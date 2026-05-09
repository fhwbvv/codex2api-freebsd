package imagestore

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ConfigJSON 是持久化在 system_settings.image_storage_config 中的 JSON shape。
//
// 使用 omitempty 是为了让 LocalBackend 的默认配置序列化结果保持简洁（仅 {"backend":"local"}）。
type ConfigJSON struct {
	Backend        string `json:"backend,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	Region         string `json:"region,omitempty"`
	Bucket         string `json:"bucket,omitempty"`
	AccessKey      string `json:"access_key,omitempty"`
	SecretKey      string `json:"secret_key,omitempty"`
	Prefix         string `json:"prefix,omitempty"`
	ForcePathStyle bool   `json:"force_path_style,omitempty"`
}

// ParseConfigJSON 把 system_settings.image_storage_config 解析为 Config。
// 空字符串 / "{}" 视为本地默认。
func ParseConfigJSON(raw string) (Config, error) {
	raw = strings.TrimSpace(raw)
	cfg := Config{Backend: BackendLocal}
	if raw == "" || raw == "{}" {
		return cfg, nil
	}
	var v ConfigJSON
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return cfg, fmt.Errorf("解析图片存储配置失败: %w", err)
	}
	cfg.Backend = v.Backend
	cfg.Endpoint = v.Endpoint
	cfg.Region = v.Region
	cfg.Bucket = v.Bucket
	cfg.AccessKey = v.AccessKey
	cfg.SecretKey = v.SecretKey
	cfg.Prefix = v.Prefix
	cfg.ForcePathStyle = v.ForcePathStyle
	return cfg.Normalize(), nil
}

// EncodeConfigJSON 反向序列化，便于 handler/前端传输（不含 LocalDir）。
func EncodeConfigJSON(cfg Config) (string, error) {
	cfg = cfg.Normalize()
	v := ConfigJSON{
		Backend:        cfg.Backend,
		Endpoint:       cfg.Endpoint,
		Region:         cfg.Region,
		Bucket:         cfg.Bucket,
		AccessKey:      cfg.AccessKey,
		SecretKey:      cfg.SecretKey,
		Prefix:         strings.TrimSuffix(cfg.Prefix, "/"), // 持久化时去掉末尾斜杠，加载时再补
		ForcePathStyle: cfg.ForcePathStyle,
	}
	if v.Backend == BackendLocal {
		// 本地后端只持久化 backend 一项，避免误存空 endpoint 等无意义字段
		v = ConfigJSON{Backend: BackendLocal}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ApplyFromJSON 解析 JSON + 调用 Configure，是 main.go / handler 的便捷入口。
//
// localDir 是 LocalBackend 用的目录（来自调用方）。即便切换到 S3，
// 也会同时构造 LocalBackend 兜底，让历史本地图继续可读。
func ApplyFromJSON(raw, localDir string) (Config, error) {
	cfg, err := ParseConfigJSON(raw)
	if err != nil {
		// 解析失败时退化到本地，避免启动期阻塞业务
		fallback := Config{Backend: BackendLocal, LocalDir: localDir}
		_ = Configure(fallback)
		return fallback, err
	}
	cfg.LocalDir = localDir
	if err := Configure(cfg); err != nil {
		// 配置失败也退化到本地
		fallback := Config{Backend: BackendLocal, LocalDir: localDir}
		_ = Configure(fallback)
		return fallback, err
	}
	return cfg, nil
}
