package imageproc

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	stdimage "image"
	"image/png"
	"runtime"
	"strings"
	"sync"

	_ "image/gif"
	_ "image/jpeg"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	UpscaleNone = ""
	Upscale2K   = "2k"
	Upscale4K   = "4k"
)

var ErrUpscaleDecode = errors.New("image upscale: decode source failed")

func NormalizeUpscale(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case Upscale2K:
		return Upscale2K
	case Upscale4K:
		return Upscale4K
	default:
		return UpscaleNone
	}
}

func UpscaleLongSide(scale string) int {
	switch NormalizeUpscale(scale) {
	case Upscale2K:
		return 2560
	case Upscale4K:
		return 3840
	default:
		return 0
	}
}

func ComputeUpscaleCacheKey(src []byte, scale string) string {
	hash := sha256.Sum256(src)
	return hex.EncodeToString(hash[:16]) + "-" + NormalizeUpscale(scale)
}

func DoUpscale(src []byte, scale string) ([]byte, string, error) {
	scale = NormalizeUpscale(scale)
	target := UpscaleLongSide(scale)
	if target == 0 || len(src) == 0 {
		return src, "", nil
	}

	srcImg, _, err := stdimage.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrUpscaleDecode, err)
	}

	bounds := srcImg.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, "", ErrUpscaleDecode
	}

	longSide := sw
	if sh > longSide {
		longSide = sh
	}
	if longSide >= target {
		return src, "", nil
	}

	dw, dh := scaledDimensions(sw, sh, target)
	dst := stdimage.NewRGBA(stdimage.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), srcImg, bounds, draw.Src, nil)

	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buf, dst); err != nil {
		return nil, "", fmt.Errorf("image upscale: png encode: %w", err)
	}
	return buf.Bytes(), "image/png", nil
}

func scaledDimensions(sw, sh, targetLongSide int) (int, int) {
	if sw >= sh {
		dw := targetLongSide
		dh := int(float64(sh) * float64(targetLongSide) / float64(sw))
		if dh < 1 {
			dh = 1
		}
		return dw, dh
	}
	dh := targetLongSide
	dw := int(float64(sw) * float64(targetLongSide) / float64(sh))
	if dw < 1 {
		dw = 1
	}
	return dw, dh
}

type UpscaleCache struct {
	mu       sync.Mutex
	items    map[string]*list.Element
	order    *list.List
	maxBytes int64
	curBytes int64
	sem      chan struct{}
}

type cacheEntry struct {
	key         string
	data        []byte
	contentType string
	size        int64
}

func NewUpscaleCache(maxBytes int64, concurrency int) *UpscaleCache {
	if maxBytes <= 0 {
		maxBytes = 512 * 1024 * 1024
	}
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
		if concurrency < 2 {
			concurrency = 2
		}
	}
	return &UpscaleCache{
		items:    make(map[string]*list.Element),
		order:    list.New(),
		maxBytes: maxBytes,
		sem:      make(chan struct{}, concurrency),
	}
}

func (c *UpscaleCache) Get(key string) ([]byte, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return nil, "", false
	}
	c.order.MoveToFront(elem)
	entry := elem.Value.(*cacheEntry)
	return entry.data, entry.contentType, true
}

func (c *UpscaleCache) Put(key string, data []byte, contentType string) {
	size := int64(len(data))
	if key == "" || size == 0 || size > c.maxBytes {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		entry := elem.Value.(*cacheEntry)
		c.curBytes -= entry.size
		entry.data = data
		entry.contentType = contentType
		entry.size = size
		c.curBytes += size
		c.order.MoveToFront(elem)
		c.evictLocked()
		return
	}

	entry := &cacheEntry{key: key, data: data, contentType: contentType, size: size}
	elem := c.order.PushFront(entry)
	c.items[key] = elem
	c.curBytes += size
	c.evictLocked()
}

func (c *UpscaleCache) evictLocked() {
	for c.curBytes > c.maxBytes {
		elem := c.order.Back()
		if elem == nil {
			return
		}
		entry := elem.Value.(*cacheEntry)
		delete(c.items, entry.key)
		c.curBytes -= entry.size
		c.order.Remove(elem)
	}
}

func (c *UpscaleCache) Acquire(ctx context.Context) error {
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *UpscaleCache) Release() {
	select {
	case <-c.sem:
	default:
	}
}

var (
	globalUpscaleCache     *UpscaleCache
	globalUpscaleCacheOnce sync.Once
)

func GlobalUpscaleCache() *UpscaleCache {
	globalUpscaleCacheOnce.Do(func() {
		globalUpscaleCache = NewUpscaleCache(512*1024*1024, 4)
	})
	return globalUpscaleCache
}
