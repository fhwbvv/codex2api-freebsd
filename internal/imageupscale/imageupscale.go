// Package imageupscale resolves the physical target size for a 2K/4K image
// request and upscales upstream base-resolution output to reach it, using the
// local resampler or an external upscaler endpoint. It is shared by the admin
// image studio and the public Images API so both surfaces give the same size
// semantics to the same model alias.
package imageupscale

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/codex2api/internal/imageproc"
)

const maxUpscalerResponseBytes = 64 << 20

// Result describes one applied upscale. Data is the re-encoded image.
type Result struct {
	Data         []byte
	ContentType  string
	Method       string
	SourceWidth  int
	SourceHeight int
	Width        int
	Height       int
}

// EnsureSize upscales imageBytes so the decoded output reaches the size the
// caller resolved from the request, going through the shared LRU cache and the
// global concurrency gate. Unlike Bytes, a parseable requestedSize is treated
// as the exact physical target — the public Images API promises the size in
// the request, not the tier long side. An explicit size may therefore trigger
// strict local resizing even without a 2K/4K tier. It returns nil when no
// resize is needed or neither a scale nor a physical size was supplied.
func EnsureSize(ctx context.Context, imageBytes []byte, scale, requestedSize string) (*Result, error) {
	return EnsureSizeWithFit(ctx, imageBytes, scale, requestedSize, "")
}

// EnsureSizeWithFit is the strict-size variant used when a request contains a
// physical WIDTHxHEIGHT target. It always produces that canvas, defaulting to
// content-preserving padding unless the caller explicitly selects cover.
func EnsureSizeWithFit(ctx context.Context, imageBytes []byte, scale, requestedSize, fit string) (*Result, error) {
	scale = imageproc.NormalizeUpscale(scale)
	targetWidth, targetHeight, exact := 0, 0, false
	if width, height, ok := parseSize(requestedSize); ok {
		targetWidth, targetHeight, exact = width, height, true
		fit = imageproc.NormalizeResizeFit(fit, true)
	}
	if len(imageBytes) == 0 || (scale == "" && !exact) {
		return nil, nil
	}
	cache := imageproc.GlobalUpscaleCache()
	key := imageproc.ComputeUpscaleCacheKey(imageBytes, scale) + "-" + strings.ToLower(strings.TrimSpace(requestedSize)) + "-" + fit
	if data, contentType, ok := cache.Get(key); ok && contentType != "" {
		return resultFromData(data, contentType, "cache", imageBytes), nil
	}
	if err := cache.Acquire(ctx); err != nil {
		return nil, err
	}
	defer cache.Release()
	if data, contentType, ok := cache.Get(key); ok && contentType != "" {
		return resultFromData(data, contentType, "cache", imageBytes), nil
	}

	if !exact {
		width, height := Dimensions(imageBytes)
		targetLongSide := imageproc.UpscaleLongSide(scale)
		if width <= 0 || height <= 0 || targetLongSide <= 0 {
			return nil, fmt.Errorf("image upscaler: invalid source or target dimensions")
		}
		targetWidth, targetHeight, exact = TargetDimensions(width, height, targetLongSide, requestedSize)
	}
	var data []byte
	var contentType, method string
	var err error
	if exact {
		data, contentType, method, err = upscaleToBoxWithFit(ctx, imageBytes, targetWidth, targetHeight, fit)
	} else {
		data, contentType, method, err = upscaleToBox(ctx, imageBytes, targetWidth, targetHeight, false)
	}
	if err != nil {
		return nil, err
	}
	if contentType == "" {
		return nil, nil
	}
	result := resultFromData(data, contentType, method, imageBytes)
	if len(data) == 0 || result.Width <= 0 || result.Height <= 0 {
		return nil, fmt.Errorf("image upscaler returned undecodable image data")
	}
	cache.Put(key, data, contentType)
	return result, nil
}

// ParseSize reports whether value is a positive WIDTHxHEIGHT image size.
func ParseSize(size string) (int, int, bool) {
	return parseSize(size)
}

func parseSize(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || width <= 0 {
		return 0, 0, false
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func resultFromData(data []byte, contentType, method string, source []byte) *Result {
	width, height := Dimensions(data)
	sourceWidth, sourceHeight := Dimensions(source)
	return &Result{
		Data:         data,
		ContentType:  contentType,
		Method:       method,
		SourceWidth:  sourceWidth,
		SourceHeight: sourceHeight,
		Width:        width,
		Height:       height,
	}
}

// Dimensions reports the decoded pixel size of data, or zeros when it cannot
// be decoded by the registered image codecs.
func Dimensions(data []byte) (int, int) {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return cfg.Width, cfg.Height
	}
	return 0, 0
}

// Bytes runs one upscale against the configured backend without touching the
// cache or the concurrency gate, resolving the target with the image-studio
// tier semantics: known preset sizes are exact, anything else scales to the
// tier long side. An empty returned content type means the backend decided no
// upscale was needed.
func Bytes(ctx context.Context, imageBytes []byte, scale, requestedSize string) ([]byte, string, string, error) {
	// Both backends resolve the same target box first. The requested size is
	// the authoritative target: asking for 2048x2048 must not produce the 2K
	// tier's 2560 long side just because that is how the tier is named.
	width, height := Dimensions(imageBytes)
	targetLongSide := imageproc.UpscaleLongSide(scale)
	if width <= 0 || height <= 0 || targetLongSide <= 0 {
		return nil, "", "", fmt.Errorf("image upscaler: invalid source or target dimensions")
	}
	targetWidth, targetHeight, exactTarget := TargetDimensions(width, height, targetLongSide, requestedSize)
	return upscaleToBox(ctx, imageBytes, targetWidth, targetHeight, exactTarget)
}

// BytesWithFit preserves Bytes' legacy behavior unless strict is requested
// with a valid physical size. Strict calls may run even without a 2K/4K tier,
// because the explicit size itself is the authoritative target.
func BytesWithFit(ctx context.Context, imageBytes []byte, scale, requestedSize, fit string, strict bool) ([]byte, string, string, error) {
	if strict {
		if targetWidth, targetHeight, ok := parseSize(requestedSize); ok {
			if width, height := Dimensions(imageBytes); width <= 0 || height <= 0 {
				return nil, "", "", fmt.Errorf("image upscaler: invalid source dimensions")
			}
			return upscaleToBoxWithFit(ctx, imageBytes, targetWidth, targetHeight, fit)
		}
	}
	return Bytes(ctx, imageBytes, scale, requestedSize)
}

// upscaleToBox dispatches one upscale toward an explicit target box to the
// local resampler or the configured external endpoint.
func upscaleToBox(ctx context.Context, imageBytes []byte, targetWidth, targetHeight int, exactTarget bool) ([]byte, string, string, error) {
	width, height := Dimensions(imageBytes)

	endpoint := strings.TrimSpace(os.Getenv("IMAGE_UPSCALER_ENDPOINT"))
	if endpoint == "" {
		data, contentType, err := imageproc.DoUpscaleTo(imageBytes, targetWidth, targetHeight, exactTarget)
		return data, contentType, "catmull-rom", err
	}

	if width >= targetWidth && height >= targetHeight {
		return nil, "", "realesrgan", nil
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, "", "", fmt.Errorf("image upscaler: invalid endpoint %q", endpoint)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/upscale"
	query := parsed.Query()
	query.Set("target_width", strconv.Itoa(targetWidth))
	query.Set("target_height", strconv.Itoa(targetHeight))
	query.Set("format", "png")
	query.Set("trigger_ratio", "1")
	// Fit inside the target box by default so a source whose aspect ratio does
	// not match the requested size is scaled down rather than cropped, matching
	// the local backend. When the ratios do match, inside and cover produce the
	// same result, so an exact requested size is still hit exactly. Operators
	// who would rather fill the box can opt into cropping with
	// IMAGE_UPSCALER_FIT=cover.
	query.Set("fit", normalizeFit(os.Getenv("IMAGE_UPSCALER_FIT")))
	parsed.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(imageBytes))
	if err != nil {
		return nil, "", "", err
	}
	request.Header.Set("Content-Type", http.DetectContentType(imageBytes))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, "", "", fmt.Errorf("call image upscaler: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxUpscalerResponseBytes+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("read image upscaler response: %w", err)
	}
	if len(body) > maxUpscalerResponseBytes {
		return nil, "", "", fmt.Errorf("image upscaler response exceeds %d bytes", maxUpscalerResponseBytes)
	}
	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(body))
		if len(message) > 1024 {
			message = message[:1024]
		}
		return nil, "", "", fmt.Errorf("image upscaler returned %d: %s", response.StatusCode, message)
	}
	applied := true
	if marker := strings.TrimSpace(response.Header.Get("X-Upscale-Applied")); marker != "" {
		parsed, parseErr := strconv.ParseBool(marker)
		if parseErr != nil {
			return nil, "", "", fmt.Errorf("image upscaler returned an invalid applied marker %q", marker)
		}
		applied = parsed
	}
	method := strings.TrimSpace(response.Header.Get("X-Upscale-Method"))
	if method == "" {
		method = "realesrgan-general-x4v3"
	}
	if !applied {
		return nil, "", method, nil
	}
	if len(body) == 0 {
		return nil, "", "", fmt.Errorf("image upscaler returned empty image data")
	}
	contentType := NormalizeContentType(response.Header.Get("Content-Type"))
	if contentType == "" {
		return nil, "", "", fmt.Errorf("image upscaler returned unsupported content type %q", response.Header.Get("Content-Type"))
	}
	return body, contentType, method, nil
}

// upscaleToBoxWithFit uses the configured backend for enlargement, then
// performs a local finalization pass so external and local backends obey the
// same exact-canvas contract.
func upscaleToBoxWithFit(ctx context.Context, imageBytes []byte, targetWidth, targetHeight int, fit string) ([]byte, string, string, error) {
	fit = imageproc.NormalizeResizeFit(fit, true)
	width, height := Dimensions(imageBytes)
	if width <= 0 || height <= 0 {
		return nil, "", "", fmt.Errorf("image upscaler: invalid source dimensions")
	}
	if width == targetWidth && height == targetHeight {
		return nil, "", "", nil
	}

	endpoint := strings.TrimSpace(os.Getenv("IMAGE_UPSCALER_ENDPOINT"))
	if endpoint == "" || (width >= targetWidth && height >= targetHeight) {
		data, contentType, err := imageproc.DoResizeTo(imageBytes, targetWidth, targetHeight, fit)
		return data, contentType, "catmull-rom-" + fit, err
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, "", "", fmt.Errorf("image upscaler: invalid endpoint %q", endpoint)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/upscale"
	query := parsed.Query()
	query.Set("target_width", strconv.Itoa(targetWidth))
	query.Set("target_height", strconv.Itoa(targetHeight))
	query.Set("format", "png")
	query.Set("trigger_ratio", "1")
	if fit == imageproc.ResizeFitCover {
		query.Set("fit", "cover")
	} else {
		query.Set("fit", "inside")
	}
	parsed.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(imageBytes))
	if err != nil {
		return nil, "", "", err
	}
	request.Header.Set("Content-Type", http.DetectContentType(imageBytes))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, "", "", fmt.Errorf("call image upscaler: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxUpscalerResponseBytes+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("read image upscaler response: %w", err)
	}
	if len(body) > maxUpscalerResponseBytes {
		return nil, "", "", fmt.Errorf("image upscaler response exceeds %d bytes", maxUpscalerResponseBytes)
	}
	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(body))
		if len(message) > 1024 {
			message = message[:1024]
		}
		return nil, "", "", fmt.Errorf("image upscaler returned %d: %s", response.StatusCode, message)
	}

	applied := true
	if marker := strings.TrimSpace(response.Header.Get("X-Upscale-Applied")); marker != "" {
		parsedApplied, parseErr := strconv.ParseBool(marker)
		if parseErr != nil {
			return nil, "", "", fmt.Errorf("image upscaler returned an invalid applied marker %q", marker)
		}
		applied = parsedApplied
	}
	method := strings.TrimSpace(response.Header.Get("X-Upscale-Method"))
	if method == "" {
		method = "realesrgan-general-x4v3"
	}
	candidate := imageBytes
	candidateType := http.DetectContentType(imageBytes)
	if applied {
		if len(body) == 0 {
			return nil, "", "", fmt.Errorf("image upscaler returned empty image data")
		}
		candidateType = NormalizeContentType(response.Header.Get("Content-Type"))
		if candidateType == "" {
			return nil, "", "", fmt.Errorf("image upscaler returned unsupported content type %q", response.Header.Get("Content-Type"))
		}
		candidate = body
	}

	final, contentType, err := imageproc.DoResizeTo(candidate, targetWidth, targetHeight, fit)
	if err != nil {
		return nil, "", "", err
	}
	if contentType != "" {
		return final, contentType, method + "+" + fit, nil
	}
	if !applied {
		return nil, "", method, nil
	}
	return candidate, candidateType, method, nil
}

// NormalizeContentType keeps the stored asset MIME type within image/*. The
// value ends up on the asset record and is echoed back inline by the asset
// route, so a compromised or man-in-the-middled upscaler must not be able to
// make the gateway serve markup from its own origin. An empty or generic
// binary type is treated as the PNG the request asked for; anything else
// outside image/* is rejected.
func NormalizeContentType(value string) string {
	mediaType := strings.ToLower(strings.TrimSpace(value))
	if index := strings.Index(mediaType, ";"); index >= 0 {
		mediaType = strings.TrimSpace(mediaType[:index])
	}
	if mediaType == "" || mediaType == "application/octet-stream" {
		return "image/png"
	}
	if strings.HasPrefix(mediaType, "image/") {
		return mediaType
	}
	return ""
}

// Backend reports which upscaler serves requests: "external" when an endpoint
// is configured, "local" otherwise.
func Backend() string {
	if strings.TrimSpace(os.Getenv("IMAGE_UPSCALER_ENDPOINT")) != "" {
		return "external"
	}
	return "local"
}

// TargetDimensions maps the source size, tier long side and the size the user
// actually requested to the target box. Known preset sizes are exact targets;
// anything else scales the source's aspect ratio to the tier long side.
func TargetDimensions(width, height, targetLongSide int, requestedSize string) (int, int, bool) {
	requestedSize = strings.ToLower(strings.TrimSpace(requestedSize))
	if targetLongSide == imageproc.UpscaleLongSide(imageproc.Upscale2K) {
		switch requestedSize {
		case "2048x2048":
			return 2048, 2048, true
		case "2560x1440":
			return 2560, 1440, true
		case "1440x2560":
			return 1440, 2560, true
		}
	}
	if targetLongSide == imageproc.UpscaleLongSide(imageproc.Upscale4K) {
		switch requestedSize {
		case "3840x2160":
			return 3840, 2160, true
		case "2160x3840":
			return 2160, 3840, true
		case "2880x2880":
			return 2880, 2880, true
		}
	}

	targetWidth := targetLongSide
	targetHeight := max(1, height*targetLongSide/width)
	if height > width {
		targetHeight = targetLongSide
		targetWidth = max(1, width*targetLongSide/height)
	}
	return targetWidth, targetHeight, false
}

func normalizeFit(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "cover") {
		return "cover"
	}
	return "inside"
}
