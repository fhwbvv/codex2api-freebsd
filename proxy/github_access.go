package proxy

import (
	"net/http"
	"net/url"
	"strings"
)

// GitHub 访问设置（issue #522）：部署方的代理链路访问 GitHub API 常被共享 IP 的
// 60 次/小时匿名配额限流，导致 CLI 版本同步、定价同步、在线更新等功能失效。
//
//	github_token      对 api.github.com 的请求附加 Authorization，配额提升为
//	                  5000 次/小时/token。token 只发给 api.github.com，绝不发给
//	                  镜像或其他主机（定价同步 URL 可被改成任意地址，不能带 token）。
//	github_proxy_url  GitHub 域名专用出站代理，与全局代理解耦：API 走脏 IP+token
//	                  即可用，大流量的 release 下载不必占用受限的干净链路。

// githubAPIHost 是唯一允许附加 token 的主机。
const githubAPIHost = "api.github.com"

// isGithubHost 判断主机是否属于 GitHub 官方域（github.com / api.github.com /
// *.githubusercontent.com）。
func isGithubHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	return host == "github.com" || host == githubAPIHost ||
		host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// GithubProxyOrDefault 返回访问 targetURL 应使用的代理：目标是 GitHub 域且配置了
// 专用代理时用专用代理，否则用调用方传入的默认代理（全局代理等）。
func GithubProxyOrDefault(targetURL, defaultProxy string) string {
	parsed, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || parsed == nil || !isGithubHost(parsed.Host) {
		return defaultProxy
	}
	if dedicated := strings.TrimSpace(CurrentRuntimeSettings().GithubProxyURL); dedicated != "" {
		return dedicated
	}
	return defaultProxy
}

// ApplyGithubAuth 仅当请求指向 api.github.com 且配置了 token 时附加 Authorization。
// 已带 Authorization 的请求不覆盖。
func ApplyGithubAuth(req *http.Request) {
	if req == nil || req.URL == nil {
		return
	}
	host := strings.ToLower(req.URL.Host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	if host != githubAPIHost || req.Header.Get("Authorization") != "" {
		return
	}
	if token := strings.TrimSpace(CurrentRuntimeSettings().GithubToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
