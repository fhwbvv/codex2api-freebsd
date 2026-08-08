package admin

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
)

const (
	inviteDefaultMaxEmails = 10
	inviteUpperMaxEmails   = 50
)

var inviteEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
var inviteEmailSplitter = regexp.MustCompile(`[,;\r\n\t ]+`)

type inviteRequest struct {
	Emails     []string `json:"emails"`
	EmailsText string   `json:"emails_text"`
	// 上游已把旧的单字段 referral_key 拆成 program_id + entrypoint，留空走默认计划。
	ProgramID  string `json:"program_id"`
	Entrypoint string `json:"entrypoint"`
	ProxyURL   string `json:"proxy_url"`
	MaxEmails  int    `json:"max_emails"`
}

// SendInvite 通过指定账号向 ChatGPT 推荐邀请端点发送邀请邮件。
// POST /api/accounts/:id/invite
func (h *Handler) SendInvite(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效的账号 ID")
		return
	}

	var req inviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}

	maxEmails := inviteDefaultMaxEmails
	if req.MaxEmails > 0 && req.MaxEmails < maxEmails {
		maxEmails = req.MaxEmails
	}
	if maxEmails > inviteUpperMaxEmails {
		maxEmails = inviteUpperMaxEmails
	}

	emails, err := collectInviteEmails(req.Emails, req.EmailsText, maxEmails)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	account := h.findAccountByID(id)
	if account == nil {
		writeError(c, http.StatusNotFound, "账号不存在")
		return
	}
	if account.GetAccessToken() == "" {
		writeError(c, http.StatusBadRequest, "账号没有可用的 access token，请先刷新账号")
		return
	}

	proxyURL := strings.TrimSpace(req.ProxyURL)
	if proxyURL == "" {
		proxyURL = h.store.ResolveProxyForAccount(account)
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()

	result, err := proxy.SendCodexInvite(ctx, account, proxyURL, req.ProgramID, req.Entrypoint, emails)
	if err != nil {
		writeError(c, http.StatusBadGateway, "邀请请求失败: "+err.Error())
		return
	}

	if !result.OK {
		// 常见：无 referral 资格的账号返回 403。透传上游响应供前端展示。
		c.JSON(http.StatusOK, gin.H{
			"ok":     false,
			"result": result,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": result,
	})
}

// GetInviteEligibility 查询指定账号的推荐邀请资格与剩余配额。
// GET /api/accounts/:id/invite/eligibility
func (h *Handler) GetInviteEligibility(c *gin.Context) {
	account, proxyURL, ok := h.resolveInviteAccount(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result, err := proxy.QueryCodexInviteEligibility(ctx, account, proxyURL,
		strings.TrimSpace(c.Query("program_id")), strings.TrimSpace(c.Query("entrypoint")))
	if err != nil {
		writeError(c, http.StatusBadGateway, "资格查询失败: "+err.Error())
		return
	}

	// 上游非 2xx 不算网关错误，透传供前端展示（如无资格账号的 403）。
	c.JSON(http.StatusOK, gin.H{"ok": result.OK, "result": result})
}

// GetInviteTracking 查询指定账号已发出的邀请及兑换状态。
// GET /api/accounts/:id/invite/tracking
func (h *Handler) GetInviteTracking(c *gin.Context) {
	account, proxyURL, ok := h.resolveInviteAccount(c)
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(strings.TrimSpace(c.Query("limit")))

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result, err := proxy.QueryCodexInviteTracking(ctx, account, proxyURL,
		strings.TrimSpace(c.Query("program_id")), strings.TrimSpace(c.Query("period")), limit)
	if err != nil {
		writeError(c, http.StatusBadGateway, "邀请记录查询失败: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": result.OK, "result": result})
}

// resolveInviteAccount 解析路径上的账号 ID、校验凭证并解出代理。
// 校验失败时已写入错误响应，返回 ok=false。
func (h *Handler) resolveInviteAccount(c *gin.Context) (*auth.Account, string, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效的账号 ID")
		return nil, "", false
	}
	account := h.findAccountByID(id)
	if account == nil {
		writeError(c, http.StatusNotFound, "账号不存在")
		return nil, "", false
	}
	if account.GetAccessToken() == "" {
		writeError(c, http.StatusBadRequest, "账号没有可用的 access token，请先刷新账号")
		return nil, "", false
	}

	proxyURL := strings.TrimSpace(c.Query("proxy_url"))
	if proxyURL == "" {
		proxyURL = h.store.ResolveProxyForAccount(account)
	}
	return account, proxyURL, true
}

// collectInviteEmails 合并 emails[] 与文本来源，去重（忽略大小写）、校验格式、夹紧上限。
func collectInviteEmails(list []string, text string, maxEmails int) ([]string, error) {
	raw := make([]string, 0, len(list))
	raw = append(raw, list...)
	if strings.TrimSpace(text) != "" {
		raw = append(raw, inviteEmailSplitter.Split(text, -1)...)
	}

	seen := make(map[string]struct{})
	emails := make([]string, 0, len(raw))
	for _, e := range raw {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		key := strings.ToLower(e)
		if _, ok := seen[key]; ok {
			continue
		}
		if !inviteEmailPattern.MatchString(e) {
			return nil, fmt.Errorf("邮箱格式不正确: %s", e)
		}
		seen[key] = struct{}{}
		emails = append(emails, e)
	}

	if len(emails) == 0 {
		return nil, fmt.Errorf("请至少提供一个邮箱")
	}
	if len(emails) > maxEmails {
		return nil, fmt.Errorf("邮箱数量超过上限 %d", maxEmails)
	}
	return emails, nil
}
