package imageproc

import (
	"bytes"
	stdimage "image"
	"image/jpeg"

	"golang.org/x/image/draw"
)

const MaxThumbKB = 64

type thumbStage struct {
	MaxWidth int
	Quality  int
}

var thumbStages = []thumbStage{
	{MaxWidth: 768, Quality: 78},
	{MaxWidth: 640, Quality: 70},
	{MaxWidth: 512, Quality: 62},
	{MaxWidth: 384, Quality: 55},
	{MaxWidth: 256, Quality: 50},
	{MaxWidth: 192, Quality: 45},
	{MaxWidth: 128, Quality: 38},
	{MaxWidth: 96, Quality: 32},
	{MaxWidth: 64, Quality: 28},
}

func ClampThumbKB(kb int) int {
	if kb <= 0 {
		return 0
	}
	if kb > MaxThumbKB {
		return MaxThumbKB
	}
	return kb
}

func MakeThumbnail(src []byte, budgetKB int) ([]byte, string, bool) {
	budgetKB = ClampThumbKB(budgetKB)
	if budgetKB <= 0 || len(src) == 0 {
		return nil, "", false
	}
	budget := budgetKB * 1024

	srcImg, _, err := stdimage.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, "", false
	}
	bounds := srcImg.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, "", false
	}

	var last []byte
	for _, stage := range thumbStages {
		out, ok := encodeThumbStage(srcImg, sw, sh, stage)
		if !ok {
			continue
		}
		last = out
		if len(out) <= budget {
			return out, "image/jpeg", true
		}
	}
	if len(last) > 0 && len(last) <= budget {
		return last, "image/jpeg", true
	}
	return nil, "", false
}

func encodeThumbStage(srcImg stdimage.Image, sw, sh int, stage thumbStage) ([]byte, bool) {
	quality := stage.Quality
	if quality < 1 {
		quality = 1
	}
	if quality > 100 {
		quality = 100
	}

	dst := srcImg
	longSide := sw
	if sh > longSide {
		longSide = sh
	}
	if longSide > stage.MaxWidth {
		dw, dh := scaledDimensions(sw, sh, stage.MaxWidth)
		canvas := stdimage.NewRGBA(stdimage.Rect(0, 0, dw, dh))
		draw.ApproxBiLinear.Scale(canvas, canvas.Bounds(), srcImg, srcImg.Bounds(), draw.Src, nil)
		dst = canvas
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}
