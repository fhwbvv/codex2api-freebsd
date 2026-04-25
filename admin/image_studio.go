package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
	"github.com/codex2api/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const defaultImageAssetDir = "/data/images"
const maxInlineImageAssetCacheBytes = 64 * 1024 * 1024

type imagePromptTemplatePayload struct {
	Name         *string  `json:"name"`
	Prompt       *string  `json:"prompt"`
	Model        *string  `json:"model"`
	Size         *string  `json:"size"`
	Quality      *string  `json:"quality"`
	OutputFormat *string  `json:"output_format"`
	Background   *string  `json:"background"`
	Style        *string  `json:"style"`
	Tags         []string `json:"tags"`
	Favorite     *bool    `json:"favorite"`
}

type imageGenerationJobPayload struct {
	Prompt       string `json:"prompt"`
	Model        string `json:"model"`
	Size         string `json:"size"`
	Quality      string `json:"quality"`
	OutputFormat string `json:"output_format"`
	Background   string `json:"background"`
	Style        string `json:"style"`
	APIKeyID     int64  `json:"api_key_id"`
	TemplateID   int64  `json:"template_id"`
}

type imageJobResponse struct {
	Job *database.ImageGenerationJob `json:"job"`
}

type imagePromptTemplatesResponse struct {
	Templates []database.ImagePromptTemplate `json:"templates"`
}

func (h *Handler) ListImagePromptTemplates(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	templates, err := h.db.ListImagePromptTemplates(ctx, c.Query("q"), c.Query("tag"))
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if templates == nil {
		templates = []database.ImagePromptTemplate{}
	}
	c.JSON(http.StatusOK, imagePromptTemplatesResponse{Templates: templates})
}

func (h *Handler) CreateImagePromptTemplate(c *gin.Context) {
	var req imagePromptTemplatePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	input, err := templateInputFromPayload(req, nil)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	id, err := h.db.InsertImagePromptTemplate(ctx, input)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	template, err := h.db.GetImagePromptTemplate(ctx, id)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"template": template})
}

func (h *Handler) UpdateImagePromptTemplate(c *gin.Context) {
	id, err := parsePositiveIDParam(c, "id")
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效 ID")
		return
	}
	var req imagePromptTemplatePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	existing, err := h.db.GetImagePromptTemplate(ctx, id)
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "模板不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	input, err := templateInputFromPayload(req, existing)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.db.UpdateImagePromptTemplate(ctx, id, input); err != nil {
		writeInternalError(c, err)
		return
	}
	template, err := h.db.GetImagePromptTemplate(ctx, id)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"template": template})
}

func (h *Handler) DeleteImagePromptTemplate(c *gin.Context) {
	id, err := parsePositiveIDParam(c, "id")
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效 ID")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.DeleteImagePromptTemplate(ctx, id); err != nil {
		writeInternalError(c, err)
		return
	}
	writeMessage(c, http.StatusOK, "已删除")
}

func templateInputFromPayload(req imagePromptTemplatePayload, existing *database.ImagePromptTemplate) (database.ImagePromptTemplateInput, error) {
	input := database.ImagePromptTemplateInput{}
	if existing != nil {
		input = database.ImagePromptTemplateInput{
			Name:         existing.Name,
			Prompt:       existing.Prompt,
			Model:        existing.Model,
			Size:         existing.Size,
			Quality:      existing.Quality,
			OutputFormat: existing.OutputFormat,
			Background:   existing.Background,
			Style:        existing.Style,
			Tags:         existing.Tags,
			Favorite:     existing.Favorite,
		}
	}
	if req.Name != nil {
		input.Name = security.SanitizeInput(*req.Name)
	}
	if req.Prompt != nil {
		input.Prompt = strings.TrimSpace(*req.Prompt)
	}
	if req.Model != nil {
		input.Model = normalizeImageStudioModel(*req.Model)
	}
	if req.Size != nil {
		input.Size = normalizeOptionalImageParam(*req.Size)
	}
	if req.Quality != nil {
		input.Quality = normalizeOptionalImageParam(*req.Quality)
	}
	if req.OutputFormat != nil {
		input.OutputFormat = normalizeOptionalImageParam(*req.OutputFormat)
	}
	if req.Background != nil {
		input.Background = normalizeOptionalImageParam(*req.Background)
	}
	if req.Style != nil {
		input.Style = normalizeOptionalImageParam(*req.Style)
	}
	if req.Tags != nil {
		input.Tags = req.Tags
	}
	if req.Favorite != nil {
		input.Favorite = *req.Favorite
	}
	if strings.TrimSpace(input.Name) == "" {
		input.Name = "未命名模板"
	}
	if len([]rune(input.Name)) > 100 {
		return input, fmt.Errorf("模板名称不能超过 100 个字符")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return input, fmt.Errorf("提示词不能为空")
	}
	if len([]rune(input.Prompt)) > 8000 {
		return input, fmt.Errorf("提示词不能超过 8000 个字符")
	}
	if input.Model == "" {
		input.Model = "gpt-image-2"
	}
	if input.OutputFormat == "" {
		input.OutputFormat = "png"
	}
	return input, nil
}

func (h *Handler) CreateImageGenerationJob(c *gin.Context) {
	var req imageGenerationJobPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		writeError(c, http.StatusBadRequest, "提示词不能为空")
		return
	}
	if len([]rune(req.Prompt)) > 8000 {
		writeError(c, http.StatusBadRequest, "提示词不能超过 8000 个字符")
		return
	}
	req.Model = normalizeImageStudioModel(req.Model)
	if req.Model == "" {
		req.Model = "gpt-image-2"
	}
	req.Size = normalizeOptionalImageParam(req.Size)
	req.Quality = normalizeOptionalImageParam(req.Quality)
	req.OutputFormat = normalizeOptionalImageParam(req.OutputFormat)
	if req.OutputFormat == "" {
		req.OutputFormat = "png"
	}
	req.Background = normalizeOptionalImageParam(req.Background)
	req.Style = normalizeOptionalImageParam(req.Style)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	apiKey, err := h.resolveImageJobAPIKey(ctx, req.APIKeyID)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	paramsJSON, _ := json.Marshal(req)
	keyID, keyName, keyMasked := imageJobAPIKeyMeta(apiKey)
	jobID, err := h.db.InsertImageGenerationJob(ctx, database.ImageGenerationJobInput{
		Prompt:       req.Prompt,
		ParamsJSON:   string(paramsJSON),
		APIKeyID:     keyID,
		APIKeyName:   keyName,
		APIKeyMasked: keyMasked,
	})
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if req.TemplateID > 0 {
		_ = h.db.IncrementImagePromptTemplateUsage(ctx, req.TemplateID)
	}
	job, err := h.db.GetImageGenerationJob(ctx, jobID)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	go h.runImageGenerationJob(jobID, req, apiKey)
	c.JSON(http.StatusOK, imageJobResponse{Job: job})
}

func (h *Handler) ListImageGenerationJobs(c *gin.Context) {
	page, pageSize := paginationParams(c, 20)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	result, err := h.db.ListImageGenerationJobs(ctx, page, pageSize)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) GetImageGenerationJob(c *gin.Context) {
	id, err := parsePositiveIDParam(c, "id")
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效 ID")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	job, err := h.db.GetImageGenerationJob(ctx, id)
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "任务不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if c.Query("include_cache") == "1" {
		h.attachImageJobAssetCachePayload(job)
	}
	c.JSON(http.StatusOK, imageJobResponse{Job: job})
}

func (h *Handler) attachImageJobAssetCachePayload(job *database.ImageGenerationJob) {
	if job == nil || len(job.Assets) == 0 {
		return
	}
	for idx := range job.Assets {
		storagePath := strings.TrimSpace(job.Assets[idx].StoragePath)
		if storagePath == "" {
			continue
		}
		info, err := os.Stat(storagePath)
		if err != nil || info.Size() <= 0 || info.Size() > maxInlineImageAssetCacheBytes {
			continue
		}
		data, err := os.ReadFile(storagePath)
		if err != nil {
			continue
		}
		job.Assets[idx].CacheB64JSON = base64.StdEncoding.EncodeToString(data)
	}
}

func (h *Handler) ListImageAssets(c *gin.Context) {
	page, pageSize := paginationParams(c, 24)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	result, err := h.db.ListImageAssets(ctx, page, pageSize)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) GetImageAssetFile(c *gin.Context) {
	id, err := parsePositiveIDParam(c, "id")
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效 ID")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	asset, err := h.db.GetImageAsset(ctx, id)
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "图片不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if _, err := os.Stat(asset.StoragePath); err != nil {
		writeError(c, http.StatusNotFound, "图片文件不存在")
		return
	}
	disposition := "inline"
	if c.Query("download") == "1" {
		disposition = "attachment"
	}
	c.Request.Header.Del("If-Modified-Since")
	c.Request.Header.Del("If-None-Match")
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	if strings.TrimSpace(asset.MimeType) != "" {
		c.Header("Content-Type", asset.MimeType)
	}
	c.Header("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, sanitizeDownloadFilename(asset.Filename)))
	c.File(asset.StoragePath)
}

func (h *Handler) DeleteImageAsset(c *gin.Context) {
	id, err := parsePositiveIDParam(c, "id")
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效 ID")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	asset, err := h.db.GetImageAsset(ctx, id)
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "图片不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if err := h.db.DeleteImageAsset(ctx, id); err != nil {
		writeInternalError(c, err)
		return
	}
	if asset.StoragePath != "" {
		_ = os.Remove(asset.StoragePath)
	}
	writeMessage(c, http.StatusOK, "已删除")
}

func (h *Handler) runImageGenerationJob(jobID int64, req imageGenerationJobPayload, apiKey *database.APIKeyRow) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	start := time.Now()
	if err := h.db.MarkImageJobRunning(ctx, jobID); err != nil {
		logImageJobError(jobID, err)
		return
	}

	rawBody, _ := buildAdminImageGenerationRequest(req)
	imageProxy := h.imageProxy
	if imageProxy == nil {
		imageProxy = proxy.NewHandler(h.store, h.db, nil, nil)
	}
	responseJSON, _, err := imageProxy.GenerateImageOnceForAdmin(ctx, rawBody, apiKey)
	durationMs := int(time.Since(start).Milliseconds())
	if err != nil {
		_ = h.db.MarkImageJobFailed(context.Background(), jobID, err.Error(), durationMs)
		return
	}

	assets, err := h.saveImageJobAssets(context.Background(), jobID, req, responseJSON)
	if err != nil {
		_ = h.db.MarkImageJobFailed(context.Background(), jobID, err.Error(), durationMs)
		return
	}
	if len(assets) == 0 {
		_ = h.db.MarkImageJobFailed(context.Background(), jobID, "上游未返回图片", durationMs)
		return
	}
	if err := h.db.MarkImageJobSucceeded(context.Background(), jobID, durationMs); err != nil {
		logImageJobError(jobID, err)
	}
}

func buildAdminImageGenerationRequest(req imageGenerationJobPayload) ([]byte, error) {
	body := map[string]any{
		"model":           req.Model,
		"prompt":          proxy.AppendImageStyleToPrompt(req.Prompt, req.Style),
		"response_format": "b64_json",
	}
	if req.Size != "" && req.Size != "auto" {
		body["size"] = req.Size
	}
	if req.Quality != "" && req.Quality != "auto" {
		body["quality"] = req.Quality
	}
	if req.OutputFormat != "" {
		body["output_format"] = req.OutputFormat
	}
	if req.Background != "" && req.Background != "auto" {
		body["background"] = req.Background
	}
	return json.Marshal(body)
}

func (h *Handler) saveImageJobAssets(ctx context.Context, jobID int64, req imageGenerationJobPayload, responseJSON []byte) ([]database.ImageAsset, error) {
	dir := imageAssetDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建图库目录失败: %w", err)
	}
	data := gjson.GetBytes(responseJSON, "data")
	if !data.IsArray() {
		return nil, fmt.Errorf("生图响应缺少 data")
	}
	responseModel := firstNonEmpty(gjson.GetBytes(responseJSON, "model").String(), req.Model)
	responseSize := firstNonEmpty(gjson.GetBytes(responseJSON, "size").String(), req.Size)
	responseQuality := firstNonEmpty(gjson.GetBytes(responseJSON, "quality").String(), req.Quality)
	responseFormat := firstNonEmpty(gjson.GetBytes(responseJSON, "output_format").String(), req.OutputFormat, "png")

	var saved []database.ImageAsset
	for idx, item := range data.Array() {
		imageBytes, mimeType, format, err := decodeImageDataItem(item)
		if err != nil {
			return saved, err
		}
		width, height := imageDimensions(imageBytes)
		actualSize := ""
		if width > 0 && height > 0 {
			actualSize = fmt.Sprintf("%dx%d", width, height)
		}
		if format == "" {
			format = responseFormat
		}
		if mimeType == "" {
			mimeType = mime.TypeByExtension("." + format)
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		filename := fmt.Sprintf("%d-%02d-%s.%s", jobID, idx+1, uuid.NewString()[:8], safeImageExtension(format, mimeType))
		storagePath := filepath.Join(dir, filename)
		if err := os.WriteFile(storagePath, imageBytes, 0o644); err != nil {
			return saved, fmt.Errorf("保存图片失败: %w", err)
		}
		input := database.ImageAssetInput{
			JobID:         jobID,
			TemplateID:    req.TemplateID,
			Filename:      filename,
			StoragePath:   storagePath,
			MimeType:      mimeType,
			Bytes:         len(imageBytes),
			Width:         width,
			Height:        height,
			Model:         responseModel,
			RequestedSize: responseSize,
			ActualSize:    actualSize,
			Quality:       responseQuality,
			OutputFormat:  format,
			RevisedPrompt: strings.TrimSpace(item.Get("revised_prompt").String()),
		}
		assetID, err := h.db.InsertImageAsset(ctx, input)
		if err != nil {
			_ = os.Remove(storagePath)
			return saved, err
		}
		asset, err := h.db.GetImageAsset(ctx, assetID)
		if err != nil {
			return saved, err
		}
		saved = append(saved, *asset)
	}
	return saved, nil
}

func (h *Handler) resolveImageJobAPIKey(ctx context.Context, id int64) (*database.APIKeyRow, error) {
	if id > 0 {
		key, err := h.db.GetAPIKeyByID(ctx, id)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("API Key 不存在")
		}
		return key, err
	}
	key, err := h.db.FirstAPIKey(ctx)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return key, err
}

func imageJobAPIKeyMeta(key *database.APIKeyRow) (int64, string, string) {
	if key == nil {
		return 0, "", ""
	}
	return key.ID, strings.TrimSpace(key.Name), security.MaskAPIKey(key.Key)
}

func decodeImageDataItem(item gjson.Result) ([]byte, string, string, error) {
	raw := strings.TrimSpace(item.Get("b64_json").String())
	mimeType := ""
	if raw == "" {
		url := strings.TrimSpace(item.Get("url").String())
		if strings.HasPrefix(strings.ToLower(url), "data:") {
			if comma := strings.Index(url, ","); comma >= 0 {
				meta := url[:comma]
				raw = url[comma+1:]
				mimeType = strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64"), ";")
			}
		}
	}
	if raw == "" {
		return nil, "", "", fmt.Errorf("生图响应缺少图片数据")
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		raw = strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(raw)
	}
	var data []byte
	var err error
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		data, err = encoding.DecodeString(raw)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("图片 Base64 解码失败: %w", err)
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	format := strings.ToLower(strings.TrimSpace(item.Get("output_format").String()))
	if format == "" {
		format = extensionFromMimeType(mimeType)
	}
	return data, mimeType, format, nil
}

func imageDimensions(data []byte) (int, int) {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return cfg.Width, cfg.Height
	}
	return 0, 0
}

func imageAssetDir() string {
	if dir := strings.TrimSpace(os.Getenv("IMAGE_ASSET_DIR")); dir != "" {
		return dir
	}
	return defaultImageAssetDir
}

func normalizeImageStudioModel(model string) string {
	model = strings.TrimSpace(model)
	switch model {
	case "", "gpt-image-2", "gpt-image-2-2k", "gpt-image-2-4k":
		return model
	default:
		return "gpt-image-2"
	}
}

func normalizeOptionalImageParam(value string) string {
	return strings.TrimSpace(value)
}

func parsePositiveIDParam(c *gin.Context, name string) (int64, error) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return id, nil
}

func paginationParams(c *gin.Context, defaultPageSize int) (int, int) {
	page := 1
	if raw := c.Query("page"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}
	pageSize := defaultPageSize
	if raw := c.Query("page_size"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			pageSize = parsed
		}
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func safeImageExtension(format string, mimeType string) string {
	format = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
	switch format {
	case "png", "jpg", "jpeg", "webp", "gif":
		if format == "jpg" {
			return "jpg"
		}
		return format
	}
	ext := strings.TrimPrefix(extensionFromMimeType(mimeType), ".")
	if ext != "" {
		return ext
	}
	return "bin"
}

func extensionFromMimeType(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch mimeType {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return ""
	}
}

func sanitizeDownloadFilename(filename string) string {
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "." || filename == "/" || filename == "" {
		return "image.png"
	}
	return strings.NewReplacer(`"`, "", "\n", "", "\r", "").Replace(filename)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func logImageJobError(jobID int64, err error) {
	if err != nil {
		log.Printf("image generation job %d failed to update state: %v", jobID, err)
	}
}
