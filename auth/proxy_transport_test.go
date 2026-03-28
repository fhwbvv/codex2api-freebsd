package auth

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func TestConfigureTransportProxyHTTPProxy(t *testing.T) {
	transport := &http.Transport{}
	baseDialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	if err := ConfigureTransportProxy(transport, "http://127.0.0.1:8080", baseDialer); err != nil {
		t.Fatalf("ConfigureTransportProxy() error = %v", err)
	}
	if transport.Proxy == nil {
		t.Fatal("expected HTTP proxy handler to be configured")
	}
	if transport.DialContext == nil {
		t.Fatal("expected HTTP proxy to preserve the base dialer")
	}
}

func TestConfigureTransportProxySOCKS5Proxy(t *testing.T) {
	transport := &http.Transport{}
	baseDialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	if err := ConfigureTransportProxy(transport, "socks5://127.0.0.1:1080", baseDialer); err != nil {
		t.Fatalf("ConfigureTransportProxy() error = %v", err)
	}
	if transport.Proxy != nil {
		t.Fatal("expected SOCKS5 proxy to bypass transport.Proxy")
	}
	if transport.DialContext == nil {
		t.Fatal("expected SOCKS5 proxy dialer to be installed")
	}
}
