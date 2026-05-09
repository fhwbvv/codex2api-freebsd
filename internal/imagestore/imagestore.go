// Package imagestore 提供图片资源的可插拔存储后端。
//
// 设计目标：
//   - 默认 LocalBackend 与现有行为完全一致（绝对路径作为 storage_path）
//   - 可选 S3Backend，覆盖 AWS S3 / Cloudflare R2 / MinIO / 阿里云 OSS / 腾讯云 COS / B2 等 S3-compatible 服务
//   - 数据库无 schema 变更：通过 storage_path 前缀（"s3://" vs 绝对路径）路由读/删，存量本地图无需迁移
package imagestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
)

const (
	BackendLocal = "local"
	BackendS3    = "s3"

	s3RefScheme = "s3://"
)

// Backend 抽象图片对象的存储读写。
type Backend interface {
	// Name 返回后端标识（"local" / "s3"），便于日志与判断。
	Name() string
	// Save 保存图片字节流，返回写入后的 ref（LocalBackend 返回绝对路径，S3Backend 返回 s3://bucket/key）。
	Save(ctx context.Context, key string, data []byte, mime string) (ref string, err error)
	// Open 以流式方式读取，调用方负责关闭。size 可能为 -1 表示未知。
	Open(ctx context.Context, ref string) (rc io.ReadCloser, size int64, err error)
	// Read 一次性读取（用于缩略图等小数据场景）。
	Read(ctx context.Context, ref string) ([]byte, error)
	// Delete 删除对应 ref。
	Delete(ctx context.Context, ref string) error
}

// Config 集中描述一份运行时配置。来源于 system_settings。
type Config struct {
	Backend        string // local / s3
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	Prefix         string // 例如 "images/"，会自动拼到对象 key 前
	ForcePathStyle bool   // MinIO / 部分自建服务必须开启
	LocalDir       string // LocalBackend 使用的目录；为空则由调用方提供默认
}

// Normalize 校正常见空白与默认值，便于与 DB 持久化值对齐。
func (c Config) Normalize() Config {
	c.Backend = strings.ToLower(strings.TrimSpace(c.Backend))
	if c.Backend == "" {
		c.Backend = BackendLocal
	}
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.Region = strings.TrimSpace(c.Region)
	c.Bucket = strings.TrimSpace(c.Bucket)
	c.AccessKey = strings.TrimSpace(c.AccessKey)
	c.SecretKey = strings.TrimSpace(c.SecretKey)
	c.Prefix = strings.TrimSpace(c.Prefix)
	if c.Prefix != "" {
		c.Prefix = strings.Trim(c.Prefix, "/") + "/"
	}
	c.LocalDir = strings.TrimSpace(c.LocalDir)
	return c
}

// Validate 在切换到 S3 前校验必要字段。
func (c Config) Validate() error {
	c = c.Normalize()
	if c.Backend == BackendLocal {
		return nil
	}
	if c.Backend != BackendS3 {
		return fmt.Errorf("unknown image storage backend: %q", c.Backend)
	}
	missing := make([]string, 0, 4)
	if c.Bucket == "" {
		missing = append(missing, "bucket")
	}
	if c.AccessKey == "" {
		missing = append(missing, "access_key")
	}
	if c.SecretKey == "" {
		missing = append(missing, "secret_key")
	}
	if len(missing) > 0 {
		return fmt.Errorf("S3 配置缺少字段: %s", strings.Join(missing, ", "))
	}
	return nil
}

// IsS3Ref 判断 storage_path 是否落在 S3 后端。
func IsS3Ref(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), s3RefScheme)
}

// state 持有当前生效的后端集合。
//
// 同时保留 local 与（可选）s3 实例，是为了让存量本地图与新图共存：
//   - 写入永远走 currentWriter（用户在系统设置里选的那个）
//   - 读取/删除按 ref 前缀分发到对应后端
type state struct {
	cfg     Config
	local   Backend
	s3      Backend // nil 表示当前未启用 S3
	primary Backend // 写入时使用
}

var current atomic.Pointer[state]

// ErrNotConfigured 表示尚未调用 Configure。
var ErrNotConfigured = errors.New("imagestore: not configured")

// Configure 应用一份新配置，原子切换全局后端。
//
// 总是会构造一个 LocalBackend 兜底，因此存量本地图始终可读。
func Configure(cfg Config) error {
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}

	local, err := NewLocalBackend(cfg.LocalDir)
	if err != nil {
		return fmt.Errorf("imagestore: 初始化本地后端失败: %w", err)
	}

	st := &state{cfg: cfg, local: local, primary: local}

	if cfg.Backend == BackendS3 {
		s3, err := NewS3Backend(cfg)
		if err != nil {
			return fmt.Errorf("imagestore: 初始化 S3 后端失败: %w", err)
		}
		st.s3 = s3
		st.primary = s3
	}

	current.Store(st)
	return nil
}

// CurrentConfig 返回当前生效的配置（拷贝）。
func CurrentConfig() Config {
	if st := current.Load(); st != nil {
		return st.cfg
	}
	return Config{Backend: BackendLocal}
}

// Primary 返回当前用于写入的后端（用户设置选中的那个）。
func Primary() (Backend, error) {
	st := current.Load()
	if st == nil {
		return nil, ErrNotConfigured
	}
	return st.primary, nil
}

// Resolve 按 ref 路由到正确后端。
//   - "s3://..." 走 S3（若 S3 未配置则报错）
//   - 其它走 Local
func Resolve(ref string) (Backend, error) {
	st := current.Load()
	if st == nil {
		return nil, ErrNotConfigured
	}
	if IsS3Ref(ref) {
		if st.s3 == nil {
			return nil, fmt.Errorf("imagestore: 当前未启用 S3，但存在 S3 ref: %s", ref)
		}
		return st.s3, nil
	}
	return st.local, nil
}

// LocalDir 返回当前 LocalBackend 使用的目录（用于 path-allowlist 校验）。
func LocalDir() string {
	if st := current.Load(); st != nil {
		if lb, ok := st.local.(*LocalBackend); ok {
			return lb.Dir()
		}
	}
	return ""
}
