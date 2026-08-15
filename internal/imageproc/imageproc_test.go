package imageproc

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestNormalizeUpscale(t *testing.T) {
	tests := map[string]string{
		"2k":      Upscale2K,
		"2K":      Upscale2K,
		" 4k ":    Upscale4K,
		"":        UpscaleNone,
		"invalid": UpscaleNone,
	}
	for input, want := range tests {
		if got := NormalizeUpscale(input); got != want {
			t.Fatalf("NormalizeUpscale(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDoUpscale(t *testing.T) {
	src := testPNG(t, 32, 16)
	out, contentType, err := DoUpscale(src, "2k")
	if err != nil {
		t.Fatalf("DoUpscale returned error: %v", err)
	}
	if contentType != "image/png" {
		t.Fatalf("contentType = %q, want image/png", contentType)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode upscaled image: %v", err)
	}
	if got := img.Bounds().Dx(); got != 2560 {
		t.Fatalf("upscaled width = %d, want 2560", got)
	}
}

func TestDoUpscaleToHitsExactTargetWhenAspectMatches(t *testing.T) {
	out, contentType, err := DoUpscaleTo(testPNG(t, 1024, 1024), 2048, 2048, true)
	if err != nil {
		t.Fatalf("DoUpscaleTo returned error: %v", err)
	}
	if contentType != "image/png" {
		t.Fatalf("contentType = %q, want image/png", contentType)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode upscaled image: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 2048 || got.Dy() != 2048 {
		t.Fatalf("upscaled size = %dx%d, want 2048x2048", got.Dx(), got.Dy())
	}
}

func TestDoUpscaleToFitsInsideWithoutCroppingMismatchedAspect(t *testing.T) {
	out, _, err := DoUpscaleTo(testPNG(t, 1024, 1024), 2560, 1440, true)
	if err != nil {
		t.Fatalf("DoUpscaleTo returned error: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode upscaled image: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 1440 || got.Dy() != 1440 {
		t.Fatalf("upscaled size = %dx%d, want 1440x1440 fitted inside the box", got.Dx(), got.Dy())
	}
}

func TestDoUpscaleToKeepsSourceThatAlreadyFillsTarget(t *testing.T) {
	src := testPNG(t, 2048, 2048)
	out, contentType, err := DoUpscaleTo(src, 2048, 2048, true)
	if err != nil {
		t.Fatalf("DoUpscaleTo returned error: %v", err)
	}
	if contentType != "" || !bytes.Equal(out, src) {
		t.Fatalf("contentType = %q, resampled = %v; want the source returned untouched", contentType, !bytes.Equal(out, src))
	}
}

func TestDoUpscaleToRejectsInvalidImage(t *testing.T) {
	if _, _, err := DoUpscaleTo([]byte("not-an-image"), 2048, 2048, true); !errors.Is(err, ErrUpscaleDecode) {
		t.Fatalf("error = %v, want ErrUpscaleDecode", err)
	}
}

func TestDoResizeToPadsToExactMismatchedCanvas(t *testing.T) {
	out, contentType, err := DoResizeTo(testPNG(t, 4, 4), 12, 6, ResizeFitPad)
	if err != nil {
		t.Fatalf("DoResizeTo returned error: %v", err)
	}
	if contentType != "image/png" {
		t.Fatalf("contentType = %q, want image/png", contentType)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode resized image: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 12 || got.Dy() != 6 {
		t.Fatalf("resized size = %dx%d, want 12x6", got.Dx(), got.Dy())
	}
	if _, _, _, alpha := img.At(0, 0).RGBA(); alpha != 0 {
		t.Fatalf("padding alpha = %d, want transparent", alpha)
	}
	if _, _, _, alpha := img.At(3, 0).RGBA(); alpha == 0 {
		t.Fatal("centered source should start after the transparent padding")
	}
}

func TestDoResizeToCoversExactMismatchedCanvas(t *testing.T) {
	out, _, err := DoResizeTo(testPNG(t, 4, 4), 12, 6, ResizeFitCover)
	if err != nil {
		t.Fatalf("DoResizeTo returned error: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode resized image: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 12 || got.Dy() != 6 {
		t.Fatalf("resized size = %dx%d, want 12x6", got.Dx(), got.Dy())
	}
	if _, _, _, alpha := img.At(0, 0).RGBA(); alpha == 0 {
		t.Fatal("cover mode should fill the complete canvas")
	}
}

func TestDoUpscaleKeepsLargeSource(t *testing.T) {
	src := testPNG(t, 2600, 16)
	out, contentType, err := DoUpscale(src, "2k")
	if err != nil {
		t.Fatalf("DoUpscale returned error: %v", err)
	}
	if contentType != "" {
		t.Fatalf("contentType = %q, want empty", contentType)
	}
	if !bytes.Equal(out, src) {
		t.Fatal("large source should be returned unchanged")
	}
}

func TestDoUpscaleRejectsInvalidImage(t *testing.T) {
	if _, _, err := DoUpscale([]byte("nope"), "2k"); err == nil {
		t.Fatal("expected invalid image error")
	}
}

func TestUpscaleCacheLRU(t *testing.T) {
	cache := NewUpscaleCache(10, 1)
	cache.Put("a", []byte("1234"), "text/plain")
	cache.Put("b", []byte("5678"), "text/plain")
	if _, _, ok := cache.Get("a"); !ok {
		t.Fatal("expected cache hit for a")
	}
	cache.Put("c", []byte("abcd"), "text/plain")
	if _, _, ok := cache.Get("b"); ok {
		t.Fatal("expected b to be evicted")
	}
	if _, _, ok := cache.Get("a"); !ok {
		t.Fatal("expected a to remain after recent access")
	}
}

func TestUpscaleCacheAcquireHonorsContext(t *testing.T) {
	cache := NewUpscaleCache(1024, 1)
	if err := cache.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cache.Acquire(ctx); err == nil {
		t.Fatal("expected context error")
	}
	cache.Release()
}

func TestMakeThumbnail(t *testing.T) {
	src := testPNG(t, 1200, 800)
	out, contentType, ok := MakeThumbnail(src, 32)
	if !ok {
		t.Fatal("MakeThumbnail returned ok=false")
	}
	if contentType != "image/jpeg" {
		t.Fatalf("contentType = %q, want image/jpeg", contentType)
	}
	if len(out) == 0 {
		t.Fatal("thumbnail is empty")
	}
	if len(out) > MaxThumbKB*1024 {
		t.Fatalf("thumbnail exceeds hard max: %d", len(out))
	}
}

func TestClampThumbKB(t *testing.T) {
	if got := ClampThumbKB(-1); got != 0 {
		t.Fatalf("negative clamp = %d, want 0", got)
	}
	if got := ClampThumbKB(MaxThumbKB + 20); got != MaxThumbKB {
		t.Fatalf("large clamp = %d, want %d", got, MaxThumbKB)
	}
}

func TestComputeUpscaleCacheKeyIncludesScale(t *testing.T) {
	key2k := ComputeUpscaleCacheKey([]byte("image"), "2k")
	key4k := ComputeUpscaleCacheKey([]byte("image"), "4k")
	if key2k == key4k {
		t.Fatal("scale should affect cache key")
	}
	if !strings.HasSuffix(key2k, "-2k") {
		t.Fatalf("unexpected key suffix: %s", key2k)
	}
}
