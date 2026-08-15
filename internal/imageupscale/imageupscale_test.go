package imageupscale

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func strictTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 80, G: 120, B: 160, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

func TestBytesWithFitHonorsStrictSizeWithoutUpscaleTier(t *testing.T) {
	t.Setenv("IMAGE_UPSCALER_ENDPOINT", "")
	data, contentType, method, err := BytesWithFit(context.Background(), strictTestPNG(t, 4, 4), "", "12x6", "pad", true)
	if err != nil {
		t.Fatalf("BytesWithFit returned error: %v", err)
	}
	if contentType != "image/png" || method != "catmull-rom-pad" {
		t.Fatalf("contentType=%q method=%q", contentType, method)
	}
	if width, height := Dimensions(data); width != 12 || height != 6 {
		t.Fatalf("strict size = %dx%d, want 12x6", width, height)
	}
}

func TestEnsureSizeWithFitSeparatesPadAndCoverCacheEntries(t *testing.T) {
	t.Setenv("IMAGE_UPSCALER_ENDPOINT", "")
	source := strictTestPNG(t, 4, 4)
	pad, err := EnsureSizeWithFit(context.Background(), source, "2k", "12x6", "pad")
	if err != nil {
		t.Fatalf("pad EnsureSizeWithFit: %v", err)
	}
	cover, err := EnsureSizeWithFit(context.Background(), source, "2k", "12x6", "cover")
	if err != nil {
		t.Fatalf("cover EnsureSizeWithFit: %v", err)
	}
	if pad == nil || cover == nil || pad.Width != 12 || pad.Height != 6 || cover.Width != 12 || cover.Height != 6 {
		t.Fatalf("pad=%#v cover=%#v", pad, cover)
	}
	if bytes.Equal(pad.Data, cover.Data) {
		t.Fatal("pad and cover results must not share a cache entry")
	}
}
