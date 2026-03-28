package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	xproxy "golang.org/x/net/proxy"
)

// ==================== rustls 0.23 + ring 指纹规格 ====================
//
// 基于真实 codex_cli_rs 的 TLS ClientHello 捕获数据：
//   JA3 Hash: 6ed79624bde98f2a10e9d013f0ea955e
//   JA4:      t13d1011h1_61a7ad8aa9b6_3fcd1a44f3e3
//
// 来源: reqwest 0.12 + rustls 0.23 + ring（无自定义配置）

// rustlsSpec 返回与 codex_cli_rs 完全一致的 TLS ClientHello 规格
func rustlsSpec() *utls.ClientHelloSpec {
	return &utls.ClientHelloSpec{
		TLSVersMax: utls.VersionTLS13,
		TLSVersMin: utls.VersionTLS12,
		CipherSuites: []uint16{
			// TLS 1.3（rustls 默认顺序）
			0x1302, // TLS_AES_256_GCM_SHA384
			0x1301, // TLS_AES_128_GCM_SHA256
			0x1303, // TLS_CHACHA20_POLY1305_SHA256
			// TLS 1.2（rustls 默认顺序）
			0xC02C, // TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384
			0xC02B, // TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
			0xCCA9, // TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
			0xC030, // TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384
			0xC02F, // TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
			0xCCA8, // TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256
			// SCSV（rustls 默认附加）
			0x00FF, // TLS_EMPTY_RENEGOTIATION_INFO_SCSV
		},
		// 扩展顺序与真实 rustls 一致（从 tls.peet.ws 捕获验证）
		Extensions: []utls.TLSExtension{
			&utls.SessionTicketExtension{},
			&utls.SNIExtension{}, // ServerName 从 Config 自动填充
			&utls.PSKKeyExchangeModesExtension{
				Modes: []uint8{1}, // psk_dhe_ke
			},
			&utls.KeyShareExtension{
				KeyShares: []utls.KeyShare{
					{Group: utls.X25519},
				},
			},
			&utls.SignatureAlgorithmsExtension{
				SupportedSignatureAlgorithms: []utls.SignatureScheme{
					0x0503, // ecdsa_secp384r1_sha384
					0x0403, // ecdsa_secp256r1_sha256
					0x0807, // ed25519
					0x0806, // rsa_pss_rsae_sha512
					0x0805, // rsa_pss_rsae_sha384
					0x0804, // rsa_pss_rsae_sha256
					0x0601, // rsa_pkcs1_sha512
					0x0501, // rsa_pkcs1_sha384
					0x0401, // rsa_pkcs1_sha256
				},
			},
			&utls.SupportedVersionsExtension{
				Versions: []uint16{
					utls.VersionTLS13,
					utls.VersionTLS12,
				},
			},
			&utls.SupportedPointsExtension{
				SupportedPoints: []byte{0}, // uncompressed
			},
			&utls.ExtendedMasterSecretExtension{},
			&utls.ALPNExtension{
				// codex_cli_rs (reqwest 无 http2 feature) 只通告 HTTP/1.1
				AlpnProtocols: []string{"http/1.1"},
			},
			&utls.SupportedCurvesExtension{
				Curves: []utls.CurveID{
					utls.X25519,
					utls.CurveP256,
					utls.CurveP384,
				},
			},
			&utls.StatusRequestExtension{},
		},
	}
}

// ==================== utls 传输层 ====================

// NewRustlsTransport 创建使用 rustls 指纹的 http.Transport
// 替代标准 crypto/tls 握手，使 TLS 指纹与真实 codex_cli_rs 一致
func NewRustlsTransport(proxyURL string) *http.Transport {
	return &http.Transport{
		// 所有 HTTPS 连接通过 utls 握手（直连和代理都在此处理）
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialWithRustlsTLS(ctx, addr, proxyURL)
		},
		// 代理由 DialTLSContext 内部处理，不使用 Transport 的代理机制
		Proxy: nil,
		// 按账号隔离的 Transport：连接池尽量精简
		// 活跃连接数由调度器 MaxConcurrency（默认 2）控制
		MaxConnsPerHost:     4,                // 留少量并发余量
		MaxIdleConns:        2,                // 每 Transport 最多 2 条空闲
		MaxIdleConnsPerHost: 1,                // 单 host 保留 1 条空闲复用
		IdleConnTimeout:     30 * time.Second, // 空闲 30s 即关闭
		// codex_cli_rs 不使用 HTTP/2
		ForceAttemptHTTP2: false,
	}
}

// dialWithRustlsTLS 建立 TCP 连接（可选代理）+ rustls TLS 握手
func dialWithRustlsTLS(ctx context.Context, addr, proxyURL string) (net.Conn, error) {
	// 1. 建立底层 TCP 连接
	tcpConn, err := dialTCP(ctx, addr, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("TCP 连接失败: %w", err)
	}

	// 2. 提取目标主机名（用于 SNI）
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		tcpConn.Close()
		return nil, fmt.Errorf("解析地址失败: %w", err)
	}

	// 3. utls 握手（rustls 指纹）
	tlsConn, err := utlsHandshake(ctx, tcpConn, host)
	if err != nil {
		tcpConn.Close()
		return nil, fmt.Errorf("TLS 握手失败: %w", err)
	}

	return tlsConn, nil
}

// utlsHandshake 执行与 rustls 指纹一致的 TLS 握手
func utlsHandshake(ctx context.Context, conn net.Conn, serverName string) (net.Conn, error) {
	config := &utls.Config{
		ServerName: serverName,
	}
	uconn := utls.UClient(conn, config, utls.HelloCustom)
	if err := uconn.ApplyPreset(rustlsSpec()); err != nil {
		return nil, fmt.Errorf("应用 TLS 指纹失败: %w", err)
	}

	// 设置握手超时
	handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := uconn.HandshakeContext(handshakeCtx); err != nil {
		return nil, err
	}

	return uconn, nil
}

// ==================== TCP 连接（支持多种代理） ====================

var defaultDialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
}

// dialTCP 建立 TCP 连接，根据代理 URL 选择直连/HTTP代理/SOCKS5
func dialTCP(ctx context.Context, targetAddr, proxyURL string) (net.Conn, error) {
	if proxyURL == "" {
		return defaultDialer.DialContext(ctx, "tcp", targetAddr)
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("解析代理 URL 失败: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return dialViaHTTPProxy(ctx, u, targetAddr)
	case "socks5", "socks5h":
		return dialViaSOCKS5(ctx, u, targetAddr)
	default:
		return nil, fmt.Errorf("不支持的代理协议: %s", u.Scheme)
	}
}

// dialViaHTTPProxy 通过 HTTP CONNECT 隧道建立 TCP 连接
func dialViaHTTPProxy(ctx context.Context, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	proxyAddr := proxyURL.Host
	if !strings.Contains(proxyAddr, ":") {
		if proxyURL.Scheme == "https" {
			proxyAddr += ":443"
		} else {
			proxyAddr += ":80"
		}
	}

	// 连接到代理服务器
	conn, err := defaultDialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("连接 HTTP 代理失败: %w", err)
	}

	// 构造 CONNECT 请求
	connectReq := &http.Request{
		Method: "CONNECT",
		URL:    &url.URL{Opaque: targetAddr},
		Host:   targetAddr,
		Header: make(http.Header),
	}

	// 代理认证
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		credentials := proxyURL.User.Username() + ":" + password
		connectReq.Header.Set("Proxy-Authorization",
			"Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	}

	if err := connectReq.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送 CONNECT 请求失败: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), connectReq)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取代理响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("代理 CONNECT 失败: %s", resp.Status)
	}

	return conn, nil
}

// dialViaSOCKS5 通过 SOCKS5 代理建立 TCP 连接
func dialViaSOCKS5(ctx context.Context, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	var auth *xproxy.Auth
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		auth = &xproxy.Auth{
			User:     proxyURL.User.Username(),
			Password: password,
		}
	}

	dialer, err := xproxy.SOCKS5("tcp", proxyURL.Host, auth, defaultDialer)
	if err != nil {
		return nil, fmt.Errorf("创建 SOCKS5 dialer 失败: %w", err)
	}

	// 优先使用 DialContext（支持超时取消）
	if cd, ok := dialer.(interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	}); ok {
		return cd.DialContext(ctx, "tcp", targetAddr)
	}

	// 回退到无 context 的 Dial（用 goroutine 包装超时）
	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		conn, err := dialer.Dial("tcp", targetAddr)
		done <- result{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-done:
		return r.conn, r.err
	}
}
