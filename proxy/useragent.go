package proxy

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// ==================== 动态 User-Agent 生成 ====================
//
// 真实 codex_cli_rs 的 UA 格式（源码: codex-rs/login/src/auth/default_client.rs）：
//   {originator}/{version} ({OS} {OS_version}; {arch}) {terminal}
//
// 示例：
//   codex_cli_rs/0.125.0 (Mac OS 15.5.0; arm64) Apple_Terminal/464
//   codex_cli_rs/0.125.0 (Mac OS 15.1.0; arm64) Ghostty/1.2.3
//   codex_cli_rs/0.125.0 (Windows 10.0.26120; x86_64) WindowsTerminal

// ClientProfile 表示一个模拟客户端的完整身份
type ClientProfile struct {
	UserAgent string // 完整的 User-Agent 字符串
	Version   string // codex CLI 版本（需与 UA 中的版本一致）
}

const (
	latestCodexCLIVersion         = "0.125.0"
	latestCodexCLIUserAgentPrefix = "codex_cli_rs/" + latestCodexCLIVersion
)

var codexOfficialClientUserAgentPrefixes = []string{
	"codex_cli_rs/",
	"codex_vscode/",
	"codex_app/",
	"codex_chatgpt_desktop/",
	"codex_atlas/",
	"codex_exec/",
	"codex_sdk_ts/",
	"codex ",
}

var codexOfficialClientOriginatorPrefixes = []string{
	"codex_",
	"codex ",
}

func IsCodexOfficialClientByHeaders(userAgent, originator string) bool {
	return matchCodexClientHeaderPrefixes(userAgent, codexOfficialClientUserAgentPrefixes) ||
		matchCodexClientHeaderPrefixes(originator, codexOfficialClientOriginatorPrefixes)
}

func LatestCodexCLIVersionForHeaders() string {
	return latestCodexCLIVersion
}

func MinimalCodexCLIUserAgentForHeaders() string {
	return latestCodexCLIUserAgentPrefix
}

func matchCodexClientHeaderPrefixes(value string, prefixes []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, prefix := range prefixes {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(value, prefix) || strings.Contains(value, prefix) {
			return true
		}
	}
	return false
}

// 预定义的真实客户端画像池
// 按开发者常见环境分布：macOS（主力） > Linux > Windows
var clientProfiles = []ClientProfile{
	// ---- macOS arm64（最常见：Apple Silicon 开发者） ----
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.5.0; arm64) Apple_Terminal/464", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.4.1; arm64) Ghostty/1.2.3", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.3.0; arm64) iTerm.app/3.5.10", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.5.0; arm64) kitty/0.40.0", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.2.0; arm64) WezTerm/20250101", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.5.0; arm64) vscode/1.100.0", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.4.0; arm64) tmux/3.5a", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 14.7.4; arm64) Alacritty/0.15.1", latestCodexCLIVersion},
	// ---- macOS x86_64（少量 Intel Mac） ----
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.4.0; x86_64) Apple_Terminal/464", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 14.7.0; x86_64) iTerm.app/3.5.8", latestCodexCLIVersion},
	// ---- Linux（服务器和开发工作站） ----
	{latestCodexCLIUserAgentPrefix + " (Ubuntu 24.04; x86_64) kitty/0.35.2", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Ubuntu 24.10; x86_64) Alacritty/0.14.0", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Arch Linux Rolling; x86_64) kitty/0.40.0", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Fedora Linux 41; x86_64) vscode/1.100.0", latestCodexCLIVersion},
	// ---- Windows ----
	{latestCodexCLIUserAgentPrefix + " (Windows 10.0.26120; x86_64) WindowsTerminal", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Windows 10.0.22631; x86_64) WindowsTerminal", latestCodexCLIVersion},
	// ---- 备用终端画像（保持最新 Codex CLI 版本） ----
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.5.0; arm64) Apple_Terminal/464", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.3.0; arm64) Ghostty/1.1.0", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Mac OS 15.4.0; arm64) vscode/1.98.0", latestCodexCLIVersion},
	{latestCodexCLIUserAgentPrefix + " (Ubuntu 24.04; x86_64) Alacritty/0.14.0", latestCodexCLIVersion},
}

// ProfileForAccount 根据账号 ID 确定性地选择一个 ClientProfile
// 同一个账号永远返回相同的 profile，不同账号大概率返回不同的 profile
func ProfileForAccount(accountID int64) ClientProfile {
	if len(clientProfiles) == 0 {
		return ClientProfile{
			UserAgent: latestCodexCLIUserAgentPrefix + " (Mac OS 15.5.0; arm64) Apple_Terminal/464",
			Version:   latestCodexCLIVersion,
		}
	}

	// 用 FNV hash 将 accountID 映射到 profile 池，确保分布均匀
	h := fnv.New32a()
	fmt.Fprintf(h, "codex2api:ua-profile:%d", accountID)
	idx := int(h.Sum32()) % len(clientProfiles)
	if idx < 0 {
		idx = -idx
	}

	return clientProfiles[idx]
}
