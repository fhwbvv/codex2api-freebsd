package admin

import (
	"context"

	"github.com/codex2api/internal/imageupscale"
)

// 超分实现已下沉到 internal/imageupscale,与公共 Images API 共享同一套
// 目标尺寸解析与本地/外部后端选择;这里保留包内旧名委托,避免大范围改动。

func upscaleImageBytes(ctx context.Context, imageBytes []byte, scale, requestedSize string) ([]byte, string, string, error) {
	return imageupscale.Bytes(ctx, imageBytes, scale, requestedSize)
}

func imageUpscalerBackend() string {
	return imageupscale.Backend()
}

func imageUpscaleTargetDimensions(width, height, targetLongSide int, requestedSize string) (int, int, bool) {
	return imageupscale.TargetDimensions(width, height, targetLongSide, requestedSize)
}

func normalizeImageUpscalerContentType(value string) string {
	return imageupscale.NormalizeContentType(value)
}
