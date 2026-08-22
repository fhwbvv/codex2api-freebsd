package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/codex2api/internal/imagestore"
	"github.com/codex2api/proxy"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
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

func TestBuildAdminImageRequestsUseCompatibleUpstreamSizeForStrictCanvas(t *testing.T) {
	req := imageGenerationJobPayload{
		Prompt:       "draw a landscape",
		Model:        "gpt-image-2",
		Size:         "1920x1080",
		OutputFormat: "png",
		InputImages:  []string{"data:image/png;base64,AA=="},
	}
	for name, build := range map[string]func(imageGenerationJobPayload) ([]byte, error){
		"generation": buildAdminImageGenerationRequest,
		"edit":       buildAdminImageEditRequest,
	} {
		t.Run(name, func(t *testing.T) {
			body, err := build(req)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if got := gjson.GetBytes(body, "size").String(); got != "1920x1088" {
				t.Fatalf("upstream size = %q, want 1920x1088", got)
			}
			if req.Size != "1920x1080" {
				t.Fatalf("requested final size mutated to %q", req.Size)
			}
		})
	}
}

func TestBuildAdminImageRequestLeavesSizeUnchangedWhenStrictDisabled(t *testing.T) {
	strict := false
	body, err := buildAdminImageGenerationRequest(imageGenerationJobPayload{
		Prompt: "draw", Model: "gpt-image-2", Size: "1920x1080", StrictSize: &strict,
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := gjson.GetBytes(body, "size").String(); got != "1920x1080" {
		t.Fatalf("upstream size = %q, want explicit opt-out size unchanged", got)
	}
}

func TestNormalizeImageJobUpscale(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		size    string
		upscale string
		want    string
		wantErr bool
	}{
		{name: "explicit 2k", model: "gpt-image-2", upscale: "2K", want: "2k"},
		{name: "2k dimensions", model: "gpt-image-2", size: "2560x1440", want: "2k"},
		{name: "4k dimensions", model: "gpt-image-2", size: "3840x2160", want: "4k"},
		{name: "4k square", model: "gpt-image-2", size: "2880x2880", want: "4k"},
		{name: "model alias", model: "gpt-image-2-4k", want: "4k"},
		{name: "native size", model: "gpt-image-2", size: "1536x1024", want: ""},
		{name: "invalid explicit", model: "gpt-image-2", upscale: "8k", wantErr: true},
		{name: "2k square dimensions", model: "gpt-image-2", size: "2048x2048", want: "2k"},
		{name: "explicit none opts out of size inference", model: "gpt-image-2", size: "2048x2048", upscale: "none", want: ""},
		{name: "explicit off opts out of model alias", model: "gpt-image-2-4k", upscale: "OFF", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeImageJobUpscale(test.model, test.size, test.upscale)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizeImageJobUpscale() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeImageJobUpscale() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("normalizeImageJobUpscale() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizePortalImageJobPayloadLocalizesBatchValidation(t *testing.T) {
	invalidUpscale := imageGenerationJobPayload{Prompt: "test", Model: "gpt-image-2", Upscale: "8k"}
	if err := normalizePortalImageJobPayload(&invalidUpscale, false); err == nil || err.Error() != "放大规格必须为 2k 或 4k" {
		t.Fatalf("invalid upscale error = %v", err)
	}
	invalidCount := imageGenerationJobPayload{Prompt: "test", Model: "gpt-image-2", N: maxImageJobOutputCount + 1}
	if err := normalizePortalImageJobPayload(&invalidCount, false); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("1 到 %d", maxImageJobOutputCount)) {
		t.Fatalf("invalid count error = %v", err)
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

func TestRunImageJobBatchAggregatesSuccessfulOutputs(t *testing.T) {
	var calls int
	response, status, partialErrors, err := runImageJobBatch(3, func() ([]byte, int, error) {
		calls++
		if calls == 2 {
			return nil, http.StatusGatewayTimeout, fmt.Errorf("timeout")
		}
		body, err := json.Marshal(map[string]any{
			"model": "gpt-image-2",
			"data":  []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte{byte(calls)})}},
		})
		return body, http.StatusOK, err
	})
	if err != nil {
		t.Fatalf("runImageJobBatch returned error: %v", err)
	}
	if status != http.StatusOK || calls != 3 {
		t.Fatalf("status=%d calls=%d", status, calls)
	}
	if len(partialErrors) != 1 || !strings.Contains(partialErrors[0], "output 2") {
		t.Fatalf("partialErrors = %#v", partialErrors)
	}
	var payload map[string]any
	if err := json.Unmarshal(response, &payload); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	data, _ := payload["data"].([]any)
	if len(data) != 2 || payload["requested_n"] != float64(3) || payload["completed_n"] != float64(2) {
		t.Fatalf("payload = %#v", payload)
	}

	calls = 0
	response, status, partialErrors, err = runImageJobBatch(3, func() ([]byte, int, error) {
		calls++
		if calls == 3 {
			return nil, http.StatusGatewayTimeout, fmt.Errorf("final timeout")
		}
		body, marshalErr := json.Marshal(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte{byte(calls)})}},
		})
		return body, http.StatusOK, marshalErr
	})
	if err != nil || len(response) == 0 {
		t.Fatalf("final failure batch response=%d error=%v", len(response), err)
	}
	if status != http.StatusOK || len(partialErrors) != 1 || !strings.Contains(partialErrors[0], "output 3") {
		t.Fatalf("final failure status=%d partialErrors=%#v", status, partialErrors)
	}
}

func TestUpscaleImageBytesUsesConfiguredRealESRGANService(t *testing.T) {
	pngBytes := tinyPNG(t)
	var gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		gotQuery = request.URL.RawQuery
		writer.Header().Set("Content-Type", "image/png")
		writer.Header().Set("X-Upscale-Applied", "true")
		writer.Header().Set("X-Upscale-Method", "realesrgan-general-x4v3")
		_, _ = writer.Write(pngBytes)
	}))
	defer server.Close()
	t.Setenv("IMAGE_UPSCALER_ENDPOINT", server.URL)

	data, contentType, method, err := upscaleImageBytes(context.Background(), pngBytes, "4k", "3840x2160")
	if err != nil {
		t.Fatalf("upscaleImageBytes returned error: %v", err)
	}
	if gotPath != "/v1/upscale" {
		t.Fatalf("path = %q", gotPath)
	}
	values, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("parse query %q: %v", gotQuery, err)
	}
	// An exact requested size is still requested exactly, but it is fitted
	// inside the box rather than cropped to fill it.
	if values.Get("target_width") != "3840" || values.Get("target_height") != "2160" || values.Get("fit") != "inside" {
		t.Fatalf("query = %q", gotQuery)
	}
	if string(data) != string(pngBytes) || contentType != "image/png" || method != "realesrgan-general-x4v3" {
		t.Fatalf("result contentType=%q method=%q bytes=%d", contentType, method, len(data))
	}
}

func TestUpscaleImageBytesAllowsMissingAppliedHeader(t *testing.T) {
	pngBytes := tinyPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write(pngBytes)
	}))
	defer server.Close()
	t.Setenv("IMAGE_UPSCALER_ENDPOINT", server.URL)

	data, contentType, _, err := upscaleImageBytes(context.Background(), pngBytes, "2k", "")
	if err != nil {
		t.Fatalf("upscaleImageBytes returned error: %v", err)
	}
	if string(data) != string(pngBytes) || contentType != "image/png" {
		t.Fatalf("result contentType=%q bytes=%d", contentType, len(data))
	}
}

func TestUpscaleImageBytesFailsWhenConfiguredServiceFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "model unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("IMAGE_UPSCALER_ENDPOINT", server.URL)

	_, _, _, err := upscaleImageBytes(context.Background(), tinyPNG(t), "2k", "")
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v, want configured service failure", err)
	}
}

func TestImageUpscaleTargetDimensionsPreservesLegacyAspectRatioWithoutRequestedSize(t *testing.T) {
	width, height, exact := imageUpscaleTargetDimensions(1536, 1024, 3840, "")
	if width != 3840 || height != 2560 || exact {
		t.Fatalf("target = %dx%d exact=%t", width, height, exact)
	}
}

func TestImageUpscaleTargetDimensionsDoesNotApplyMismatchedRequestedSize(t *testing.T) {
	width, height, exact := imageUpscaleTargetDimensions(1536, 1024, 2560, "3840x2160")
	if width != 2560 || height != 1706 || exact {
		t.Fatalf("target = %dx%d exact=%t", width, height, exact)
	}
}

func TestSaveImageJobAssetsPersistsFilesAndMetadata(t *testing.T) {
	db := newTestAdminDB(t)
	dir := t.TempDir()
	t.Setenv("IMAGE_ASSET_DIR", dir)
	if err := imagestore.Configure(imagestore.Config{Backend: imagestore.BackendLocal, LocalDir: dir}); err != nil {
		t.Fatalf("imagestore.Configure: %v", err)
	}
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

	assets, warnings, err := handler.saveImageJobAssets(context.Background(), jobID, imageGenerationJobPayload{
		Model:        "gpt-image-2",
		Size:         "auto",
		Quality:      "high",
		OutputFormat: "png",
		TemplateID:   12,
	}, raw)
	if err != nil {
		t.Fatalf("saveImageJobAssets 返回错误: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
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

func TestSaveImageJobAssetsPreservesOriginalWhenUpscalerUnavailable(t *testing.T) {
	db := newTestAdminDB(t)
	dir := t.TempDir()
	t.Setenv("IMAGE_ASSET_DIR", dir)
	if err := imagestore.Configure(imagestore.Config{Backend: imagestore.BackendLocal, LocalDir: dir}); err != nil {
		t.Fatalf("imagestore.Configure: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("IMAGE_UPSCALER_ENDPOINT", server.URL)

	jobID, err := db.InsertImageGenerationJob(context.Background(), database.ImageGenerationJobInput{Prompt: "degraded upscale"})
	if err != nil {
		t.Fatalf("InsertImageGenerationJob: %v", err)
	}
	pngBytes := tinyPNG(t)
	raw, err := json.Marshal(map[string]any{
		"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngBytes)}},
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	assets, warnings, err := (&Handler{db: db}).saveImageJobAssets(context.Background(), jobID, imageGenerationJobPayload{
		Model: "gpt-image-2", Upscale: "2k", OutputFormat: "png",
	}, raw)
	if err != nil {
		t.Fatalf("saveImageJobAssets returned error: %v", err)
	}
	if len(assets) != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "original image preserved") {
		t.Fatalf("assets=%d warnings=%#v", len(assets), warnings)
	}
	if assets[0].Bytes != len(pngBytes) || assets[0].Width != 1 || assets[0].Height != 1 {
		t.Fatalf("asset = %#v", assets[0])
	}
}

func TestUpscaleImageBytesHonoursRequestedSizeOnLocalBackend(t *testing.T) {
	t.Setenv("IMAGE_UPSCALER_ENDPOINT", "")

	data, contentType, method, err := upscaleImageBytes(context.Background(), squarePNG(t, 1024), "2k", "2048x2048")
	if err != nil {
		t.Fatalf("upscaleImageBytes returned error: %v", err)
	}
	if contentType != "image/png" || method != "catmull-rom" {
		t.Fatalf("contentType = %q, method = %q", contentType, method)
	}
	width, height := imageDimensions(data)
	if width != 2048 || height != 2048 {
		t.Fatalf("local upscale produced %dx%d, want the requested 2048x2048", width, height)
	}
}

func TestSaveImageJobAssetsDefaultsExplicitSizeToStrictPad(t *testing.T) {
	db := newTestAdminDB(t)
	dir := t.TempDir()
	t.Setenv("IMAGE_ASSET_DIR", dir)
	t.Setenv("IMAGE_UPSCALER_ENDPOINT", "")
	if err := imagestore.Configure(imagestore.Config{Backend: imagestore.BackendLocal, LocalDir: dir}); err != nil {
		t.Fatalf("imagestore.Configure: %v", err)
	}
	jobID, err := db.InsertImageGenerationJob(context.Background(), database.ImageGenerationJobInput{Prompt: "strict landscape"})
	if err != nil {
		t.Fatalf("InsertImageGenerationJob: %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"size": "4x4",
		"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(squarePNG(t, 4))}},
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	assets, warnings, err := (&Handler{db: db}).saveImageJobAssets(context.Background(), jobID, imageGenerationJobPayload{
		Model: "gpt-image-2", Size: "12x6", OutputFormat: "png",
	}, raw)
	if err != nil {
		t.Fatalf("saveImageJobAssets returned error: %v", err)
	}
	if len(warnings) != 0 || len(assets) != 1 {
		t.Fatalf("assets=%d warnings=%#v", len(assets), warnings)
	}
	if asset := assets[0]; asset.Width != 12 || asset.Height != 6 || asset.ActualSize != "12x6" || asset.RequestedSize != "12x6" {
		t.Fatalf("asset = %#v", asset)
	}
}

func TestSaveImageJobAssetsPreservesOriginalWhenUpscalerReturnsInvalidImage(t *testing.T) {
	db := newTestAdminDB(t)
	dir := t.TempDir()
	t.Setenv("IMAGE_ASSET_DIR", dir)
	if err := imagestore.Configure(imagestore.Config{Backend: imagestore.BackendLocal, LocalDir: dir}); err != nil {
		t.Fatalf("imagestore.Configure: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "image/png")
		writer.Header().Set("X-Upscale-Applied", "true")
		_, _ = writer.Write([]byte("not-an-image"))
	}))
	defer server.Close()
	t.Setenv("IMAGE_UPSCALER_ENDPOINT", server.URL)

	jobID, err := db.InsertImageGenerationJob(context.Background(), database.ImageGenerationJobInput{Prompt: "invalid upscale batch"})
	if err != nil {
		t.Fatalf("InsertImageGenerationJob: %v", err)
	}
	// Two outputs so a failure on one cannot be confused with an empty batch.
	encoded := base64.StdEncoding.EncodeToString(tinyPNG(t))
	raw, err := json.Marshal(map[string]any{
		"data": []map[string]string{{"b64_json": encoded}, {"b64_json": encoded}},
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	assets, warnings, err := (&Handler{db: db}).saveImageJobAssets(context.Background(), jobID, imageGenerationJobPayload{
		Model: "gpt-image-2", Upscale: "2k", OutputFormat: "png", N: 2,
	}, raw)
	if err != nil {
		t.Fatalf("saveImageJobAssets returned error: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("assets = %d, want both already-billed outputs preserved", len(assets))
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %#v, want one degraded warning per output", warnings)
	}
	for _, warning := range warnings {
		if !strings.Contains(warning, "original image preserved") {
			t.Fatalf("warning = %q, want the original-preserved notice", warning)
		}
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
	if err := imagestore.Configure(imagestore.Config{Backend: imagestore.BackendLocal, LocalDir: dir}); err != nil {
		t.Fatalf("imagestore.Configure: %v", err)
	}
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

func TestDeleteImageGenerationJobRouteDeletesAssetsAndFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	defer tc.Close()
	store := auth.NewStore(db, tc, nil)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	router := gin.New()
	handler.RegisterRoutes(router)

	dir := t.TempDir()
	if err := imagestore.Configure(imagestore.Config{Backend: imagestore.BackendLocal, LocalDir: dir}); err != nil {
		t.Fatalf("imagestore.Configure: %v", err)
	}
	jobID, err := db.InsertImageGenerationJob(context.Background(), database.ImageGenerationJobInput{Prompt: "delete job"})
	if err != nil {
		t.Fatalf("InsertImageGenerationJob 返回错误: %v", err)
	}
	if err := db.MarkImageJobSucceeded(context.Background(), jobID, 123); err != nil {
		t.Fatalf("MarkImageJobSucceeded 返回错误: %v", err)
	}
	path := filepath.Join(dir, "job-asset.png")
	if err := os.WriteFile(path, tinyPNG(t), 0o644); err != nil {
		t.Fatalf("write asset file: %v", err)
	}
	assetID, err := db.InsertImageAsset(context.Background(), database.ImageAssetInput{
		JobID:       jobID,
		Filename:    "job-asset.png",
		StoragePath: path,
		MimeType:    "image/png",
		Bytes:       len(tinyPNG(t)),
	})
	if err != nil {
		t.Fatalf("InsertImageAsset 返回错误: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/images/jobs/"+strconv.FormatInt(jobID, 10), nil)
	req.Header.Set("X-Admin-Key", "admin-secret")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := db.GetImageGenerationJob(context.Background(), jobID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetImageGenerationJob after delete err = %v, want sql.ErrNoRows", err)
	}
	if _, err := db.GetImageAsset(context.Background(), assetID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetImageAsset after delete err = %v, want sql.ErrNoRows", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("asset file stat err = %v, want not exist", err)
	}
}

func TestExternalImageJobRoutesCreateAndQueryOwnJob(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	defer tc.Close()
	store := auth.NewStore(db, tc, nil)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	imageProxy := proxy.NewHandler(store, db, nil, nil)
	imageProxy.SetRuntimeCache(tc)
	router := gin.New()
	handler.RegisterExternalImageRoutes(router, imageProxy)

	keyID, err := db.InsertAPIKey(context.Background(), "external", "sk-external")
	if err != nil {
		t.Fatalf("InsertAPIKey 返回错误: %v", err)
	}

	body := strings.NewReader(`{"prompt":"draw a cat","model":"gpt-image-2","size":"auto","quality":"auto","output_format":"png"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/jobs", body)
	req.Header.Set("Authorization", "Bearer sk-external")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 body=%s", recorder.Code, recorder.Body.String())
	}
	var created imageJobResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.Job == nil || created.Job.ID <= 0 || created.Job.APIKeyID != keyID || created.Job.APIKeyName != "external" {
		t.Fatalf("created job = %#v, want api key metadata", created.Job)
	}
	if created.Job.Status != database.ImageJobQueued {
		t.Fatalf("job status = %q, want queued", created.Job.Status)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/images/jobs/"+strconv.FormatInt(created.Job.ID, 10), nil)
	getReq.Header.Set("Authorization", "Bearer sk-external")
	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(getRecorder, getReq)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200 body=%s", getRecorder.Code, getRecorder.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := db.GetImageGenerationJob(context.Background(), created.Job.ID)
		if err != nil {
			t.Fatalf("GetImageGenerationJob: %v", err)
		}
		if job.Status != database.ImageJobQueued && job.Status != database.ImageJobRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("image job status = %q, want terminal state before cleanup", job.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExternalImageJobRejectsBatchThatWouldCrossAPIKeyRPM(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	t.Cleanup(func() { _ = tc.Close() })
	store := auth.NewStore(db, tc, nil)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	imageProxy := proxy.NewHandler(store, db, nil, nil)
	imageProxy.SetRuntimeCache(tc)
	router := gin.New()
	handler.RegisterExternalImageRoutes(router, imageProxy)

	const apiKeyValue = "sk-image-batch-rpm"
	_, err := db.InsertAPIKeyWithOptions(context.Background(), database.APIKeyInput{
		Name: "image-batch-rpm",
		Key:  apiKeyValue,
		Limits: database.APIKeyLimits{
			RPM: 2,
		},
	})
	if err != nil {
		t.Fatalf("InsertAPIKeyWithOptions: %v", err)
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/jobs", strings.NewReader(`{"prompt":"draw a cat","model":"gpt-image-2","n":3}`))
	req.Header.Set("Authorization", "Bearer "+apiKeyValue)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusTooManyRequests || !strings.Contains(recorder.Body.String(), "batch of 3") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	page, err := db.ListImageGenerationJobs(context.Background(), 1, 20, 0)
	if err != nil {
		t.Fatalf("ListImageGenerationJobs: %v", err)
	}
	if len(page.Jobs) != 0 {
		t.Fatalf("rate-limited batch persisted jobs: %+v", page.Jobs)
	}
}

func TestExternalImageJobPromptGuardBlocksBeforeExternalWork(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		http.Error(w, "unexpected upstream request", http.StatusBadGateway)
	}))
	t.Cleanup(upstream.Close)
	previousResin := proxy.GetResinConfig()
	proxy.SetResinConfig(&proxy.ResinConfig{BaseURL: upstream.URL, PlatformName: "image-job-guard-test"})
	t.Cleanup(func() { proxy.SetResinConfig(previousResin) })
	previousRuntime := proxy.CurrentRuntimeSettings()
	nextRuntime := previousRuntime
	nextRuntime.CodexForceWebsocket = false
	proxy.ApplyRuntimeSettings(nextRuntime)
	t.Cleanup(func() { proxy.ApplyRuntimeSettings(previousRuntime) })

	var remoteFetchCalls atomic.Int32
	previousDialer := dialPublicExternalInputImageAddress
	dialPublicExternalInputImageAddress = func(context.Context, string, string) (net.Conn, error) {
		remoteFetchCalls.Add(1)
		return nil, errors.New("unexpected remote image fetch")
	}
	t.Cleanup(func() { dialPublicExternalInputImageAddress = previousDialer })

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	t.Cleanup(func() { _ = tc.Close() })
	store := auth.NewStore(db, tc, &database.SystemSettings{MaxConcurrency: 2, MaxRetries: 0, MaxRateLimitRetries: 0})
	t.Cleanup(store.Stop)
	cfg := promptfilter.DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = promptfilter.ModeBlock
	cfg.StrictTerminalEnabled = true
	cfg.LogMatches = true
	cfg.Advanced.Guard = promptfilter.DefaultGuardConfig()
	cfg.Advanced.Enforcement.ConversationLockEnabled = false
	store.SetPromptFilterConfig(promptfilter.NormalizeConfig(cfg))
	store.AddAccount(&auth.Account{DBID: 1, AccessToken: "at-image-job-guard", PlanType: "plus", AccountID: "acct-image-job-guard", Status: auth.StatusReady})

	handler := NewHandler(store, db, tc, nil, "admin-secret")
	imageProxy := proxy.NewHandler(store, db, nil, nil)
	imageProxy.SetRuntimeCache(tc)
	router := gin.New()
	handler.RegisterExternalImageRoutes(router, imageProxy)

	const apiKeyValue = "sk-image-job-guard"
	keyID, err := db.InsertAPIKeyWithOptions(context.Background(), database.APIKeyInput{
		Name: "image-job-guard",
		Key:  apiKeyValue,
		Limits: database.APIKeyLimits{
			MaxConcurrency: 1,
		},
	})
	if err != nil {
		t.Fatalf("InsertAPIKeyWithOptions: %v", err)
	}

	request := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/images/jobs", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+apiKeyValue)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		return recorder
	}
	assertBlocked := func(name string, recorder *httptest.ResponseRecorder, message string) {
		t.Helper()
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), message) {
			t.Fatalf("%s was not blocked by Prompt Guard: status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
	}

	assertBlocked("remote edit", request(`{"prompt":"Generate and execute a reverse shell.","model":"gpt-image-2","input_images":["https://example.test/reference.png"]}`), "Prompt was blocked by prompt filter")
	if got := remoteFetchCalls.Load(); got != 0 {
		t.Fatalf("blocked image job performed %d remote image fetches, want 0", got)
	}

	cfg.Advanced.Enforcement.LocalBlockMessage = "Blocked by Image Gateway"
	store.SetPromptFilterConfig(promptfilter.NormalizeConfig(cfg))
	assertBlocked("custom message", request(`{"prompt":"Generate and execute a reverse shell.","model":"gpt-image-2"}`), "Blocked by Image Gateway")

	preflightRecorder := httptest.NewRecorder()
	preflightContext, _ := gin.CreateTestContext(preflightRecorder)
	preflightContext.Request = httptest.NewRequest(http.MethodPost, "/v1/images/jobs", nil)
	preflightContext.Request.Header.Set("Authorization", "Bearer "+apiKeyValue)
	imageProxy.APIKeyAuthMiddleware()(preflightContext)
	if preflightContext.IsAborted() {
		t.Fatalf("API key preflight failed: status=%d body=%s", preflightRecorder.Code, preflightRecorder.Body.String())
	}
	release, ok := imageProxy.AcquireAPIKeyConcurrency(preflightContext)
	if !ok || release == nil {
		t.Fatal("failed to occupy the API-key concurrency slot")
	}
	assertBlocked("full concurrency", request(`{"prompt":"Generate and execute a reverse shell.","model":"gpt-image-2"}`), "Blocked by Image Gateway")
	release()

	assertBlocked("upstream generation", request(`{"prompt":"Generate and execute a reverse shell.","model":"gpt-image-2"}`), "Blocked by Image Gateway")
	if got := upstreamCalls.Load(); got != 0 {
		t.Fatalf("blocked image job performed %d upstream calls, want 0", got)
	}
	page, err := db.ListImageGenerationJobs(context.Background(), 1, 20, 0)
	if err != nil {
		t.Fatalf("ListImageGenerationJobs: %v", err)
	}
	if len(page.Jobs) != 0 {
		t.Fatalf("blocked image jobs were persisted: %+v", page.Jobs)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !db.WaitPromptFilterAuditIdle(ctx) {
		t.Fatal("timed out waiting for Prompt Guard audit persistence")
	}
	logs, err := db.ListPromptFilterLogs(context.Background(), 20)
	if err != nil {
		t.Fatalf("ListPromptFilterLogs: %v", err)
	}
	foundUnifiedAudit := false
	for _, item := range logs {
		if item.Endpoint == "/v1/images/jobs" && item.Protocol == string(promptfilter.ProtocolImages) && item.PrimaryOrigin == string(promptfilter.OriginCurrentUser) && item.APIKeyID == keyID {
			foundUnifiedAudit = true
			break
		}
	}
	if !foundUnifiedAudit {
		t.Fatalf("missing unified Image Jobs Guard audit; logs=%+v", logs)
	}
}

func TestExternalImageJobRouteRejectsOtherAPIKeyJob(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	defer tc.Close()
	store := auth.NewStore(db, tc, nil)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	imageProxy := proxy.NewHandler(store, db, nil, nil)
	imageProxy.SetRuntimeCache(tc)
	router := gin.New()
	handler.RegisterExternalImageRoutes(router, imageProxy)

	ownerID, err := db.InsertAPIKey(context.Background(), "owner", "sk-owner")
	if err != nil {
		t.Fatalf("InsertAPIKey owner 返回错误: %v", err)
	}
	if _, err := db.InsertAPIKey(context.Background(), "other", "sk-other"); err != nil {
		t.Fatalf("InsertAPIKey other 返回错误: %v", err)
	}
	jobID, err := db.InsertImageGenerationJob(context.Background(), database.ImageGenerationJobInput{
		Prompt:       "private job",
		ParamsJSON:   `{}`,
		APIKeyID:     ownerID,
		APIKeyName:   "owner",
		APIKeyMasked: "sk-o...wner",
	})
	if err != nil {
		t.Fatalf("InsertImageGenerationJob 返回错误: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/images/jobs/"+strconv.FormatInt(jobID, 10), nil)
	req.Header.Set("Authorization", "Bearer sk-other")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestExternalImageJobRouteRequiresAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	defer tc.Close()
	store := auth.NewStore(db, tc, nil)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	imageProxy := proxy.NewHandler(store, db, nil, nil)
	imageProxy.SetRuntimeCache(tc)
	router := gin.New()
	handler.RegisterExternalImageRoutes(router, imageProxy)
	if _, err := db.InsertAPIKey(context.Background(), "existing", "sk-existing"); err != nil {
		t.Fatalf("InsertAPIKey 返回错误: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/images/jobs", strings.NewReader(`{"prompt":"draw"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestExternalImageJobRouteEnforcesModelAllowList(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	defer tc.Close()
	store := auth.NewStore(db, tc, nil)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	imageProxy := proxy.NewHandler(store, db, nil, nil)
	imageProxy.SetRuntimeCache(tc)
	router := gin.New()
	handler.RegisterExternalImageRoutes(router, imageProxy)

	if _, err := db.InsertAPIKeyWithOptions(context.Background(), database.APIKeyInput{
		Name: "limited",
		Key:  "sk-limited",
		Limits: database.APIKeyLimits{
			ModelAllow: []string{"gpt-5.4"},
		},
	}); err != nil {
		t.Fatalf("InsertAPIKeyWithOptions 返回错误: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/images/jobs", strings.NewReader(`{"prompt":"draw","model":"gpt-image-2"}`))
	req.Header.Set("Authorization", "Bearer sk-limited")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestExternalImageJobRouteNormalizesEditPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	defer tc.Close()
	store := auth.NewStore(db, tc, nil)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	imageProxy := proxy.NewHandler(store, db, nil, nil)
	imageProxy.SetRuntimeCache(tc)
	router := gin.New()
	handler.RegisterExternalImageRoutes(router, imageProxy)

	keyID, err := db.InsertAPIKey(context.Background(), "external-edit", "sk-external-edit")
	if err != nil {
		t.Fatalf("InsertAPIKey 返回错误: %v", err)
	}

	body := strings.NewReader(`{"prompt":" edit this ","model":"unknown-model","input_images":["","data:image/png;base64,aGk="],"output_format":""}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/jobs", body)
	req.Header.Set("Authorization", "Bearer sk-external-edit")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 body=%s", recorder.Code, recorder.Body.String())
	}
	var created imageJobResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.Job == nil || created.Job.APIKeyID != keyID {
		t.Fatalf("created job = %#v, want api key id %d", created.Job, keyID)
	}
	var params imageGenerationJobPayload
	if err := json.Unmarshal([]byte(created.Job.ParamsJSON), &params); err != nil {
		t.Fatalf("decode params_json: %v", err)
	}
	if params.Prompt != "edit this" || params.Model != "gpt-image-2" || params.OutputFormat != "png" {
		t.Fatalf("params = %#v, want normalized prompt/model/output format", params)
	}
	if len(params.InputImages) != 1 || params.InputImages[0] != "data:image/png;base64,aGk=" {
		t.Fatalf("input_images = %#v, want trimmed single image", params.InputImages)
	}
}

func TestExternalImageJobRouteRejectsPrivateInputImageURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	defer tc.Close()
	store := auth.NewStore(db, tc, nil)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	imageProxy := proxy.NewHandler(store, db, nil, nil)
	imageProxy.SetRuntimeCache(tc)
	router := gin.New()
	handler.RegisterExternalImageRoutes(router, imageProxy)

	if _, err := db.InsertAPIKey(context.Background(), "external-private-url", "sk-private-url"); err != nil {
		t.Fatalf("InsertAPIKey 返回错误: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/images/jobs", strings.NewReader(`{"prompt":"edit","input_images":["http://127.0.0.1/private.png"]}`))
	req.Header.Set("Authorization", "Bearer sk-private-url")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestExternalImageJobRouteFetchesPublicInputImageAsDataURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tiny := tinyPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tiny)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	oldDialer := dialPublicExternalInputImageAddress
	dialPublicExternalInputImageAddress = func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(serverURL.Hostname(), port))
	}
	defer func() { dialPublicExternalInputImageAddress = oldDialer }()

	db := newTestAdminDB(t)
	tc := cache.NewMemory(1)
	defer tc.Close()
	store := auth.NewStore(db, tc, nil)
	handler := NewHandler(store, db, tc, nil, "admin-secret")
	imageProxy := proxy.NewHandler(store, db, nil, nil)
	imageProxy.SetRuntimeCache(tc)
	router := gin.New()
	handler.RegisterExternalImageRoutes(router, imageProxy)

	if _, err := db.InsertAPIKey(context.Background(), "external-fetch-url", "sk-fetch-url"); err != nil {
		t.Fatalf("InsertAPIKey 返回错误: %v", err)
	}

	body := fmt.Sprintf(`{"prompt":"edit","input_images":["http://example.com:%s/source.png"]}`, serverURL.Port())
	req := httptest.NewRequest(http.MethodPost, "/v1/images/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-fetch-url")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 body=%s", recorder.Code, recorder.Body.String())
	}
	var created imageJobResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.Job == nil {
		t.Fatalf("created job is nil")
	}
	var params imageGenerationJobPayload
	if err := json.Unmarshal([]byte(created.Job.ParamsJSON), &params); err != nil {
		t.Fatalf("decode params_json: %v", err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(tiny)
	if len(params.InputImages) != 1 || params.InputImages[0] != want {
		t.Fatalf("input_images = %#v, want fetched data URL", params.InputImages)
	}
}

func squarePNG(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGNgYPgPAAEDAQC0wS7EAAAAAElFTkSuQmCC")
	if err != nil {
		t.Fatalf("decode tiny png: %v", err)
	}
	return data
}
