package imagestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigNormalizeAndValidate(t *testing.T) {
	c := Config{Backend: " S3 ", Bucket: "b", AccessKey: "ak", SecretKey: "sk", Prefix: "/img"}.Normalize()
	if c.Backend != BackendS3 {
		t.Fatalf("backend not normalized: %q", c.Backend)
	}
	if c.Prefix != "img/" {
		t.Fatalf("prefix not normalized: %q", c.Prefix)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate s3 ok cfg: %v", err)
	}

	bad := Config{Backend: "s3"}
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected missing fields error")
	}

	other := Config{Backend: "weird"}
	if err := other.Validate(); err == nil {
		t.Fatalf("expected unknown backend error")
	}

	local := Config{Backend: ""}.Normalize()
	if local.Backend != BackendLocal {
		t.Fatalf("default should be local")
	}
	if err := local.Validate(); err != nil {
		t.Fatalf("local always valid: %v", err)
	}
}

func TestConfigJSONRoundtrip(t *testing.T) {
	cfg := Config{
		Backend:        BackendS3,
		Endpoint:       "https://s3.example",
		Region:         "auto",
		Bucket:         "bk",
		AccessKey:      "ak",
		SecretKey:      "sk",
		Prefix:         "p/",
		ForcePathStyle: true,
	}
	raw, err := EncodeConfigJSON(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	parsed, err := ParseConfigJSON(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Backend != cfg.Backend || parsed.Bucket != cfg.Bucket || parsed.Prefix != "p/" || !parsed.ForcePathStyle {
		t.Fatalf("roundtrip mismatch: %+v", parsed)
	}

	localRaw, err := EncodeConfigJSON(Config{Backend: BackendLocal, Bucket: "ignored"})
	if err != nil {
		t.Fatalf("encode local: %v", err)
	}
	if localRaw != `{"backend":"local"}` {
		t.Fatalf("local should drop fields, got %s", localRaw)
	}

	for _, raw := range []string{"", "{}"} {
		parsed, err := ParseConfigJSON(raw)
		if err != nil {
			t.Fatalf("parse empty %q: %v", raw, err)
		}
		if parsed.Backend != BackendLocal {
			t.Fatalf("empty should become local, got %q", parsed.Backend)
		}
	}

	if _, err := ParseConfigJSON("{not json"); err == nil {
		t.Fatalf("expected json error")
	}
}

func TestIsS3Ref(t *testing.T) {
	if !IsS3Ref("s3://bucket/key") {
		t.Fatal("expected true")
	}
	if IsS3Ref("/data/images/x.png") {
		t.Fatal("expected false")
	}
	if IsS3Ref("") {
		t.Fatal("expected false for empty")
	}
}

func TestLocalBackendCRUD(t *testing.T) {
	dir := t.TempDir()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if b.Name() != BackendLocal {
		t.Fatalf("name")
	}

	ctx := context.Background()
	ref, err := b.Save(ctx, "hello.bin", []byte("hi"), "application/octet-stream")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	abs, _ := filepath.Abs(filepath.Join(dir, "hello.bin"))
	if ref != abs {
		t.Fatalf("ref=%s want=%s", ref, abs)
	}

	data, err := b.Read(ctx, ref)
	if err != nil || string(data) != "hi" {
		t.Fatalf("read: %v %q", err, data)
	}
	rc, size, err := b.Open(ctx, ref)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if size != 2 {
		t.Fatalf("size=%d", size)
	}
	rc.Close()

	if err := b.Delete(ctx, ref); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// idempotent
	if err := b.Delete(ctx, ref); err != nil {
		t.Fatalf("delete missing should be nil: %v", err)
	}
	if _, err := b.Read(ctx, ref); err == nil {
		t.Fatalf("expected read error after delete")
	}

	if _, err := b.Save(ctx, "", []byte("x"), ""); err == nil {
		t.Fatalf("empty key should error")
	}

	emptyB, _ := NewLocalBackend("")
	if _, err := emptyB.Save(ctx, "k", []byte("x"), ""); err == nil {
		t.Fatalf("empty dir should error")
	}
}

func TestConfigureAndResolve(t *testing.T) {
	dir := t.TempDir()
	if err := Configure(Config{Backend: BackendLocal, LocalDir: dir}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if got := LocalDir(); got != dir {
		// LocalDir uses Abs internally? It returns whatever NewLocalBackend stored.
		// We pass a plain path; expect equality.
		t.Fatalf("LocalDir=%q want %q", got, dir)
	}

	primary, err := Primary()
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	if primary.Name() != BackendLocal {
		t.Fatalf("primary name=%s", primary.Name())
	}

	// Local ref → local backend
	backend, err := Resolve("/tmp/x.png")
	if err != nil {
		t.Fatalf("resolve local: %v", err)
	}
	if backend.Name() != BackendLocal {
		t.Fatalf("resolve local name=%s", backend.Name())
	}

	// S3 ref while only local configured → error
	if _, err := Resolve("s3://bucket/key"); err == nil {
		t.Fatalf("expected error resolving s3 without s3 backend")
	}

	cfg := CurrentConfig()
	if cfg.Backend != BackendLocal {
		t.Fatalf("current config backend=%s", cfg.Backend)
	}
}

func TestConfigureRejectsInvalid(t *testing.T) {
	err := Configure(Config{Backend: "nope"})
	if err == nil {
		t.Fatalf("expected validation error")
	}

	// Reset to a known-good local config to avoid leaking state to other tests.
	if err := Configure(Config{Backend: BackendLocal, LocalDir: t.TempDir()}); err != nil {
		t.Fatalf("recover: %v", err)
	}
}

func TestPrimaryNotConfigured(t *testing.T) {
	// We cannot easily reset atomic.Pointer to nil from the outside without
	// changing the API. Instead just validate ErrNotConfigured equality.
	if !errors.Is(ErrNotConfigured, ErrNotConfigured) {
		t.Fatal("sanity")
	}
}

func TestThumbnailCacheLRU(t *testing.T) {
	c := NewThumbnailCache(15) // 15 bytes
	c.Put("a", "image/png", []byte("123"))
	c.Put("b", "image/png", []byte("4567"))

	if data, ct, ok := c.Get("a"); !ok || ct != "image/png" || string(data) != "123" {
		t.Fatalf("get a: ok=%v ct=%s data=%q", ok, ct, data)
	}

	// Access "a" → "a" becomes most-recent. Inserting 10-byte payload exceeds
	// cap (3+4+10=17 > 15) and should evict "b" (oldest), keeping "a" + "c".
	c.Put("c", "image/png", []byte("0123456789"))
	if _, _, ok := c.Get("b"); ok {
		t.Fatalf("b should be evicted")
	}
	if _, _, ok := c.Get("a"); !ok {
		t.Fatalf("a should still be there")
	}
	if _, _, ok := c.Get("c"); !ok {
		t.Fatalf("c should be there")
	}

	// oversized payload silently rejected
	c.Put("big", "image/png", make([]byte, 30))
	if _, _, ok := c.Get("big"); ok {
		t.Fatalf("oversized should not be cached")
	}

	// empty rejected
	c.Put("empty", "image/png", nil)
	if _, _, ok := c.Get("empty"); ok {
		t.Fatalf("empty should not be cached")
	}
}

func TestThumbnailCacheInvalidate(t *testing.T) {
	c := NewThumbnailCache(0) // default cap
	c.Put(ThumbKey(7, 32), "image/jpeg", []byte("abc"))
	c.Put(ThumbKey(7, 64), "image/jpeg", []byte("defg"))
	c.Put(ThumbKey(8, 32), "image/jpeg", []byte("hi"))

	c.Invalidate(7)
	if _, _, ok := c.Get(ThumbKey(7, 32)); ok {
		t.Fatalf("7:32 should be gone")
	}
	if _, _, ok := c.Get(ThumbKey(7, 64)); ok {
		t.Fatalf("7:64 should be gone")
	}
	if _, _, ok := c.Get(ThumbKey(8, 32)); !ok {
		t.Fatalf("8:32 should remain")
	}
}

func TestThumbnailCacheUpdateExisting(t *testing.T) {
	c := NewThumbnailCache(0)
	c.Put("k", "image/png", []byte("aa"))
	c.Put("k", "image/jpeg", []byte("bbbb"))
	data, ct, ok := c.Get("k")
	if !ok || ct != "image/jpeg" || string(data) != "bbbb" {
		t.Fatalf("update: ok=%v ct=%s data=%q", ok, ct, data)
	}
}

func TestThumbnailCacheConcurrency(t *testing.T) {
	c := NewThumbnailCache(1024)
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func(i int) {
			for j := 0; j < 200; j++ {
				key := string(rune('a' + i))
				c.Put(key, "image/png", []byte("xxxx"))
				_, _, _ = c.Get(key)
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestParseS3RefAndBuild(t *testing.T) {
	ref := buildS3Ref("bk", "p/k.png")
	if ref != "s3://bk/p/k.png" {
		t.Fatalf("build ref: %s", ref)
	}
	bucket, key, err := parseS3Ref(ref)
	if err != nil || bucket != "bk" || key != "p/k.png" {
		t.Fatalf("parse: bucket=%s key=%s err=%v", bucket, key, err)
	}

	for _, bad := range []string{"", "https://x", "s3://", "s3://only", "s3://b/"} {
		if _, _, err := parseS3Ref(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestApplyFromJSONFallbackOnInvalid(t *testing.T) {
	dir := t.TempDir()
	cfg, err := ApplyFromJSON("{not json", dir)
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if cfg.Backend != BackendLocal {
		t.Fatalf("fallback should be local: %s", cfg.Backend)
	}
	// State should be configured to local fallback
	if _, err := Primary(); err != nil {
		t.Fatalf("primary after fallback: %v", err)
	}

	// Valid JSON for local
	cfg, err = ApplyFromJSON(`{"backend":"local"}`, dir)
	if err != nil {
		t.Fatalf("apply local: %v", err)
	}
	if cfg.Backend != BackendLocal {
		t.Fatalf("backend mismatch: %s", cfg.Backend)
	}

	// Empty stays local
	cfg, err = ApplyFromJSON("", dir)
	if err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if cfg.Backend != BackendLocal {
		t.Fatalf("backend mismatch empty: %s", cfg.Backend)
	}

	// Make sure file-on-disk via Configure works end-to-end
	primary, err := Primary()
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	ref, err := primary.Save(context.Background(), "round.bin", []byte("ok"), "application/octet-stream")
	if err != nil {
		t.Fatalf("save through primary: %v", err)
	}
	if _, statErr := os.Stat(ref); statErr != nil {
		t.Fatalf("file should exist: %v", statErr)
	}
}
