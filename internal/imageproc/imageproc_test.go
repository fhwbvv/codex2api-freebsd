package imageproc

import (
	"bytes"
	"context"
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
