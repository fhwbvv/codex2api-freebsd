package admin

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

const (
	defaultPromptFilterBindingSecretGraceSeconds = int64(300)
	minGeneratedBindingSecretGraceSeconds        = int64(60)
	maxPromptFilterBindingSecretGraceSeconds     = int64(86400)
	promptFilterBindingMutationTimeout           = 5 * time.Second
)

type promptFilterNewAPIBindingCreateRequest struct {
	APIKeyID              int64  `json:"api_key_id"`
	PlatformCode          string `json:"platform_code"`
	PlatformName          string `json:"platform_name"`
	Enabled               *bool  `json:"enabled"`
	RequireSignedIdentity *bool  `json:"require_signed_identity"`
	PromptFilterScope     string `json:"prompt_filter_scope"`
}

type promptFilterNewAPIBindingUpdateRequest struct {
	PlatformCode          *string `json:"platform_code"`
	PlatformName          *string `json:"platform_name"`
	Enabled               *bool   `json:"enabled"`
	RequireSignedIdentity *bool   `json:"require_signed_identity"`
	PromptFilterScope     *string `json:"prompt_filter_scope"`
}

type promptFilterNewAPIBindingSecretRequest struct {
	Secret       string `json:"secret"`
	GraceSeconds *int64 `json:"grace_seconds"`
}

type promptFilterNewAPIBindingResponse struct {
	APIKeyID                int64      `json:"api_key_id"`
	PlatformCode            string     `json:"platform_code"`
	PlatformName            string     `json:"platform_name"`
	Enabled                 bool       `json:"enabled"`
	RequireSignedIdentity   bool       `json:"require_signed_identity"`
	PromptFilterScope       string     `json:"prompt_filter_scope"`
	SecretConfigured        bool       `json:"secret_configured"`
	SecretMasked            string     `json:"secret_masked"`
	PreviousSecretActive    bool       `json:"previous_secret_active"`
	PreviousSecretExpiresAt *time.Time `json:"previous_secret_expires_at,omitempty"`
	UpdatedAt               time.Time  `json:"updated_at"`
	Secret                  string     `json:"secret,omitempty"`
}

func (h *Handler) ListPromptFilterNewAPIBindings(c *gin.Context) {
	bindings, err := h.db.ListPromptFilterNewAPIBindings(c.Request.Context())
	if err != nil {
		writeInternalError(c, err)
		return
	}
	items := make([]promptFilterNewAPIBindingResponse, 0, len(bindings))
	for _, binding := range bindings {
		items = append(items, newPromptFilterNewAPIBindingResponse(binding, ""))
	}
	c.JSON(http.StatusOK, gin.H{"bindings": items})
}

func (h *Handler) GetPromptFilterNewAPIBinding(c *gin.Context) {
	apiKeyID, ok := promptFilterBindingAPIKeyID(c)
	if !ok {
		return
	}
	binding, err := h.db.GetPromptFilterNewAPIBinding(c.Request.Context(), apiKeyID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(c, http.StatusNotFound, "平台绑定不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, newPromptFilterNewAPIBindingResponse(binding, ""))
}

func (h *Handler) CreatePromptFilterNewAPIBinding(c *gin.Context) {
	var req promptFilterNewAPIBindingCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式无效")
		return
	}
	if _, err := h.db.GetAPIKeyByID(c.Request.Context(), req.APIKeyID); errors.Is(err, sql.ErrNoRows) {
		writeError(c, http.StatusNotFound, "API Key 不存在")
		return
	} else if err != nil {
		writeInternalError(c, err)
		return
	}
	code, name, ok := validatePromptFilterBindingFields(c, req.PlatformCode, req.PlatformName)
	if !ok {
		return
	}
	secret, err := generatePromptFilterBindingSecret()
	if err != nil {
		writeError(c, http.StatusInternalServerError, "生成随机密钥失败")
		return
	}
	enabled, requireSigned := true, false
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.RequireSignedIdentity != nil {
		requireSigned = *req.RequireSignedIdentity
	}
	scope, validScope := database.NormalizePromptFilterScope(req.PromptFilterScope)
	if !validScope {
		writeError(c, http.StatusBadRequest, "Prompt 检查范围仅支持 inherit、local_only 或 off")
		return
	}
	binding := &database.PromptFilterNewAPIBinding{
		APIKeyID: req.APIKeyID, PlatformCode: code, PlatformName: name, Secret: secret,
		Enabled: enabled, RequireSignedIdentity: requireSigned, PromptFilterScope: scope,
	}
	mutationCtx, cancelMutation := promptFilterBindingMutationContext(c)
	defer cancelMutation()
	h.settingsUpdateMu.Lock()
	err = h.db.CreatePromptFilterNewAPIBinding(mutationCtx, binding)
	if err == nil {
		binding.UpdatedAt = time.Now().UTC()
		h.store.UpsertPromptFilterNewAPIBinding(*binding)
	}
	h.settingsUpdateMu.Unlock()
	if errors.Is(err, database.ErrPromptFilterNewAPIBindingConflict) {
		writeError(c, http.StatusConflict, "API Key 或平台代码已存在绑定")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusCreated, newPromptFilterNewAPIBindingResponse(binding, secret))
}

func (h *Handler) UpdatePromptFilterNewAPIBinding(c *gin.Context) {
	apiKeyID, ok := promptFilterBindingAPIKeyID(c)
	if !ok {
		return
	}
	var req promptFilterNewAPIBindingUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式无效")
		return
	}
	mutationCtx, cancelMutation := promptFilterBindingMutationContext(c)
	defer cancelMutation()
	h.settingsUpdateMu.Lock()
	binding, err := h.db.GetPromptFilterNewAPIBinding(mutationCtx, apiKeyID)
	if errors.Is(err, sql.ErrNoRows) {
		h.settingsUpdateMu.Unlock()
		writeError(c, http.StatusNotFound, "平台绑定不存在")
		return
	}
	if err != nil {
		h.settingsUpdateMu.Unlock()
		writeInternalError(c, err)
		return
	}
	if req.PlatformCode != nil {
		binding.PlatformCode = *req.PlatformCode
	}
	if req.PlatformName != nil {
		binding.PlatformName = *req.PlatformName
	}
	if req.Enabled != nil {
		binding.Enabled = *req.Enabled
	}
	if req.RequireSignedIdentity != nil {
		binding.RequireSignedIdentity = *req.RequireSignedIdentity
	}
	if req.PromptFilterScope != nil {
		scope, validScope := database.NormalizePromptFilterScope(*req.PromptFilterScope)
		if !validScope {
			h.settingsUpdateMu.Unlock()
			writeError(c, http.StatusBadRequest, "Prompt 检查范围仅支持 inherit、local_only 或 off")
			return
		}
		binding.PromptFilterScope = scope
	}
	binding.PlatformCode, binding.PlatformName, ok = validatePromptFilterBindingFields(c, binding.PlatformCode, binding.PlatformName)
	if !ok {
		h.settingsUpdateMu.Unlock()
		return
	}
	err = h.db.UpdatePromptFilterNewAPIBinding(mutationCtx, binding)
	if err == nil {
		binding.UpdatedAt = time.Now().UTC()
		h.store.UpsertPromptFilterNewAPIBinding(*binding)
	}
	h.settingsUpdateMu.Unlock()
	if errors.Is(err, database.ErrPromptFilterNewAPIBindingConflict) {
		writeError(c, http.StatusConflict, "平台代码已被其他 API Key 使用")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, newPromptFilterNewAPIBindingResponse(binding, ""))
}

func (h *Handler) GeneratePromptFilterNewAPIBindingSecret(c *gin.Context) {
	req := promptFilterNewAPIBindingSecretRequest{}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "请求格式无效")
			return
		}
	}
	grace := defaultPromptFilterBindingSecretGraceSeconds
	if req.GraceSeconds != nil {
		grace = *req.GraceSeconds
	}
	if grace < minGeneratedBindingSecretGraceSeconds || grace > maxPromptFilterBindingSecretGraceSeconds {
		writeError(c, http.StatusBadRequest, "随机生成密钥时 grace_seconds 必须在 60 到 86400 之间，避免响应中断后旧密钥立即失效")
		return
	}
	secret, err := generatePromptFilterBindingSecret()
	if err != nil {
		writeError(c, http.StatusInternalServerError, "生成随机密钥失败")
		return
	}
	h.replacePromptFilterNewAPIBindingSecret(c, secret, &grace)
}

func (h *Handler) ReplacePromptFilterNewAPIBindingSecret(c *gin.Context) {
	var req promptFilterNewAPIBindingSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式无效")
		return
	}
	secret := strings.TrimSpace(req.Secret)
	if len(secret) < 32 {
		writeError(c, http.StatusBadRequest, "审计绑定密钥至少需要 32 个字符")
		return
	}
	h.replacePromptFilterNewAPIBindingSecret(c, secret, req.GraceSeconds)
}

func (h *Handler) replacePromptFilterNewAPIBindingSecret(c *gin.Context, secret string, graceSeconds *int64) {
	apiKeyID, ok := promptFilterBindingAPIKeyID(c)
	if !ok {
		return
	}
	grace := defaultPromptFilterBindingSecretGraceSeconds
	if graceSeconds != nil {
		grace = *graceSeconds
	}
	if grace < 0 || grace > maxPromptFilterBindingSecretGraceSeconds {
		writeError(c, http.StatusBadRequest, "grace_seconds 必须在 0 到 86400 之间")
		return
	}
	mutationCtx, cancelMutation := promptFilterBindingMutationContext(c)
	defer cancelMutation()
	h.settingsUpdateMu.Lock()
	binding, err := h.db.GetPromptFilterNewAPIBinding(mutationCtx, apiKeyID)
	if errors.Is(err, sql.ErrNoRows) {
		h.settingsUpdateMu.Unlock()
		writeError(c, http.StatusNotFound, "平台绑定不存在")
		return
	}
	if err != nil {
		h.settingsUpdateMu.Unlock()
		writeInternalError(c, err)
		return
	}
	updatedAt := time.Now().UTC()
	var previousExpiresAt *time.Time
	if grace > 0 {
		expiresAt := updatedAt.Add(time.Duration(grace) * time.Second)
		previousExpiresAt = &expiresAt
	}
	err = h.db.ReplacePromptFilterNewAPIBindingSecretAt(mutationCtx, apiKeyID, secret, previousExpiresAt)
	if err == nil {
		if previousExpiresAt == nil {
			binding.PreviousSecret = ""
			binding.PreviousSecretExpiresAt = nil
		} else {
			binding.PreviousSecret = binding.Secret
			binding.PreviousSecretExpiresAt = previousExpiresAt
		}
		binding.Secret = secret
		binding.UpdatedAt = updatedAt
		h.store.UpsertPromptFilterNewAPIBinding(*binding)
	}
	h.settingsUpdateMu.Unlock()
	if errors.Is(err, sql.ErrNoRows) {
		writeError(c, http.StatusNotFound, "平台绑定不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, newPromptFilterNewAPIBindingResponse(binding, secret))
}

func (h *Handler) DeletePromptFilterNewAPIBinding(c *gin.Context) {
	apiKeyID, ok := promptFilterBindingAPIKeyID(c)
	if !ok {
		return
	}
	mutationCtx, cancelMutation := promptFilterBindingMutationContext(c)
	defer cancelMutation()
	h.settingsUpdateMu.Lock()
	err := h.db.DeletePromptFilterNewAPIBinding(mutationCtx, apiKeyID)
	if err == nil {
		h.store.RemovePromptFilterNewAPIBinding(apiKeyID)
	}
	h.settingsUpdateMu.Unlock()
	if errors.Is(err, sql.ErrNoRows) {
		writeError(c, http.StatusNotFound, "平台绑定不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "平台绑定已删除"})
}

func promptFilterBindingMutationContext(c *gin.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if c != nil && c.Request != nil {
		base = context.WithoutCancel(c.Request.Context())
	}
	return context.WithTimeout(base, promptFilterBindingMutationTimeout)
}

func promptFilterBindingAPIKeyID(c *gin.Context) (int64, bool) {
	apiKeyID, err := strconv.ParseInt(strings.TrimSpace(c.Param("api_key_id")), 10, 64)
	if err != nil || apiKeyID <= 0 {
		writeError(c, http.StatusBadRequest, "无效的 API Key ID")
		return 0, false
	}
	return apiKeyID, true
}

func validatePromptFilterBindingFields(c *gin.Context, code, name string) (string, string, bool) {
	var codeOK bool
	code, codeOK = database.NormalizePromptFilterPlatformCode(code)
	if !codeOK {
		writeError(c, http.StatusBadRequest, "platform_code 只能包含小写字母、数字、下划线或短横线，且最长 32 字符")
		return "", "", false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = code
	}
	if len([]rune(name)) > 255 {
		writeError(c, http.StatusBadRequest, "platform_name 最长 255 字符")
		return "", "", false
	}
	return code, name, true
}

func generatePromptFilterBindingSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func maskPromptFilterBindingSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) < 12 {
		return "********"
	}
	return secret[:6] + "…" + secret[len(secret)-6:]
}

func newPromptFilterNewAPIBindingResponse(binding *database.PromptFilterNewAPIBinding, reveal string) promptFilterNewAPIBindingResponse {
	if binding == nil {
		return promptFilterNewAPIBindingResponse{}
	}
	previousActive := binding.PreviousSecret != "" && binding.PreviousSecretExpiresAt != nil && binding.PreviousSecretExpiresAt.After(time.Now())
	return promptFilterNewAPIBindingResponse{
		APIKeyID: binding.APIKeyID, PlatformCode: binding.PlatformCode, PlatformName: binding.PlatformName,
		Enabled: binding.Enabled, RequireSignedIdentity: binding.RequireSignedIdentity, PromptFilterScope: binding.PromptFilterScope,
		SecretConfigured: binding.Secret != "", SecretMasked: maskPromptFilterBindingSecret(binding.Secret),
		PreviousSecretActive: previousActive, PreviousSecretExpiresAt: binding.PreviousSecretExpiresAt,
		UpdatedAt: binding.UpdatedAt, Secret: reveal,
	}
}
