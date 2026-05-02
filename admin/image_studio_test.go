package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

func TestBuildAdminImageGenerationRequestOmitsAutoSize(t *testing.T) {
	body, err := buildAdminImageGenerationRequest(imageGenerationJobPayload{
		Prompt:       "draw a city wallpaper",
		Model:        "gpt-image-2-4k",
		Size:         "auto",
		Quality:      "high",
		OutputFormat: "png",
		Background:   "auto",
		Style:        "cinematic",
	})
	if err != nil {
		t.Fatalf("buildAdminImageGenerationRequest 返回错误: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if payload["model"] != "gpt-image-2-4k" || payload["response_format"] != "b64_json" {
		t.Fatalf("payload = %#v", payload)
	}
	if _, exists := payload["size"]; exists {
		t.Fatalf("auto size should be omitted, payload = %#v", payload)
	}
	if _, exists := payload["background"]; exists {
		t.Fatalf("auto background should be omitted, payload = %#v", payload)
	}
	if _, exists := payload["style"]; exists {
		t.Fatalf("style should be folded into prompt instead of sent as an API parameter, payload = %#v", payload)
	}
	if prompt := payload["prompt"].(string); !strings.Contains(prompt, "Style guidance: cinematic") {
		t.Fatalf("prompt = %q, want style guidance appended", prompt)
	}
	if payload["quality"] != "high" || payload["output_format"] != "png" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestImageJobJPEGFallbackDecision(t *testing.T) {
	req := imageGenerationJobPayload{OutputFormat: "png"}
	if !shouldFallbackImageJobToJPEG(req, http.StatusBadGateway, fmt.Errorf("upstream image generation failed (server_error): An error occurred while processing your request")) {
		t.Fatalf("expected PNG server_error to fall back to JPEG")
	}
	openAIProcessingErr := "upstream image generation failed (server_error): An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID e412a5aa-0f63-45a9-bef9-f856ec589574 in your message."
	if !shouldFallbackImageJobToJPEG(req, http.StatusBadGateway, fmt.Errorf("%s", openAIProcessingErr)) {
		t.Fatalf("expected OpenAI processing server_error to fall back to JPEG")
	}
	if shouldFallbackImageJobToJPEG(req, http.StatusTooManyRequests, fmt.Errorf("rate limit reached")) {
		t.Fatalf("rate limit should not fall back to JPEG")
	}
	if shouldFallbackImageJobToJPEG(imageGenerationJobPayload{OutputFormat: "jpeg"}, http.StatusBadGateway, fmt.Errorf("server_error")) {
		t.Fatalf("non-PNG format should not fall back to JPEG")
	}

	fallback := jpegFallbackImageJobRequest(imageGenerationJobPayload{OutputFormat: "png", Background: "transparent"})
	if fallback.OutputFormat != "jpeg" || fallback.Background != "opaque" {
		t.Fatalf("fallback request = %#v, want jpeg with opaque background", fallback)
	}
}

func TestSaveImageJobAssetsPersistsFilesAndMetadata(t *testing.T) {
	db := newTestAdminDB(t)
	dir := t.TempDir()
	t.Setenv("IMAGE_ASSET_DIR", dir)
	handler := &Handler{db: db}

	jobID, err := db.InsertImageGenerationJob(context.Background(), database.ImageGenerationJobInput{Prompt: "a blue square"})
	if err != nil {
		t.Fatalf("InsertImageGenerationJob 返回错误: %v", err)
	}
	pngBytes := tinyPNG(t)
	response := map[string]any{
		"model":         "gpt-image-2",
		"size":          "1024x1024",
		"quality":       "high",
		"output_format": "png",
		"data": []map[string]any{
			{
				"b64_json":       base64.StdEncoding.EncodeToString(pngBytes),
				"revised_prompt": "a revised blue square",
			},
		},
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	assets, err := handler.saveImageJobAssets(context.Background(), jobID, imageGenerationJobPayload{
		Model:        "gpt-image-2",
		Size:         "auto",
		Quality:      "high",
		OutputFormat: "png",
		TemplateID:   12,
	}, raw)
	if err != nil {
		t.Fatalf("saveImageJobAssets 返回错误: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("len(assets) = %d, want 1", len(assets))
	}
	asset := assets[0]
	if asset.JobID != jobID || asset.TemplateID != 12 || asset.MimeType != "image/png" || asset.Bytes != len(pngBytes) {
		t.Fatalf("asset = %#v", asset)
	}
	if asset.Width != 1 || asset.Height != 1 || asset.ActualSize != "1x1" || asset.RequestedSize != "1024x1024" {
		t.Fatalf("asset dimensions/size = %#v", asset)
	}
	if _, err := os.Stat(asset.StoragePath); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
	if !strings.HasPrefix(asset.StoragePath, dir+string(os.PathSeparator)) {
		t.Fatalf("storage path = %q, want under %q", asset.StoragePath, dir)
	}
}

func TestImageAssetFileRouteRequiresAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	defer tc.Close()
	store := auth.NewStore(db, tc, nil)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	router := gin.New()
	handler.RegisterRoutes(router)

	jobID, err := db.InsertImageGenerationJob(context.Background(), database.ImageGenerationJobInput{Prompt: "asset"})
	if err != nil {
		t.Fatalf("InsertImageGenerationJob 返回错误: %v", err)
	}
	dir := t.TempDir()
	pngBytes := tinyPNG(t)
	path := filepath.Join(dir, "asset.png")
	if err := os.WriteFile(path, pngBytes, 0o644); err != nil {
		t.Fatalf("write asset file: %v", err)
	}
	assetID, err := db.InsertImageAsset(context.Background(), database.ImageAssetInput{
		JobID:         jobID,
		Filename:      "asset.png",
		StoragePath:   path,
		MimeType:      "image/png",
		Bytes:         len(pngBytes),
		Width:         1,
		Height:        1,
		Model:         "gpt-image-2",
		RequestedSize: "1024x1024",
		ActualSize:    "1x1",
		OutputFormat:  "png",
	})
	if err != nil {
		t.Fatalf("InsertImageAsset 返回错误: %v", err)
	}

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/admin/images/assets/1/file", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/images/assets/"+strconv.FormatInt(assetID, 10)+"/file", nil)
	req.Header.Set("X-Admin-Key", "admin-secret")
	req.Header.Set("If-Modified-Since", time.Now().Add(24*time.Hour).UTC().Format(http.TimeFormat))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/png") {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	if got := recorder.Body.Bytes(); string(got) != string(pngBytes) {
		t.Fatalf("file bytes = %v, want %v", got, pngBytes)
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGNgYPgPAAEDAQC0wS7EAAAAAElFTkSuQmCC")
	if err != nil {
		t.Fatalf("decode tiny png: %v", err)
	}
	return data
}
