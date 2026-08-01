package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	xproxy "golang.org/x/net/proxy"

	"github.com/codex2api/security"
)

type contextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type proxyConnectDialer interface {
	Dial(network, address string) (net.Conn, error)
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type observingProxyDialer struct {
	base      *net.Dialer
	onConnect func()
	wrapConn  func(net.Conn) net.Conn
}

func (d *observingProxyDialer) Dial(network, address string) (net.Conn, error) {
	conn, err := d.base.Dial(network, address)
	if err == nil {
		conn = d.observe(conn)
	}
	return conn, err
}

func (d *observingProxyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := d.base.DialContext(ctx, network, address)
	if err == nil {
		conn = d.observe(conn)
	}
	return conn, err
}

func (d *observingProxyDialer) observe(conn net.Conn) net.Conn {
	if d.onConnect != nil {
		d.onConnect()
	}
	if d.wrapConn != nil {
		return d.wrapConn(conn)
	}
	return conn
}

type observingSOCKSConn struct {
	net.Conn
	onTargetConnectStart func()
	once                 sync.Once
}

func (c *observingSOCKSConn) Write(payload []byte) (int, error) {
	n, err := c.Conn.Write(payload)
	if err == nil && n == len(payload) && isSOCKSConnectRequest(payload) {
		c.once.Do(c.onTargetConnectStart)
	}
	return n, err
}

func isSOCKSConnectRequest(payload []byte) bool {
	if len(payload) < 4 || payload[0] != 0x05 || payload[1] != 0x01 || payload[2] != 0x00 {
		return false
	}
	switch payload[3] {
	case 0x01:
		return len(payload) == 4+net.IPv4len+2
	case 0x03:
		return len(payload) >= 5 && len(payload) == 7+int(payload[4])
	case 0x04:
		return len(payload) == 4+net.IPv6len+2
	default:
		return false
	}
}

// ProxyTransportObserver reports connection milestones while configuring a
// transport proxy.
type ProxyTransportObserver struct {
	OnProxyConnect            func()
	OnSOCKSTargetConnectStart func()
}

// ConfigureTransportProxy applies HTTP(S) or SOCKS5 proxy settings to a transport.
func ConfigureTransportProxy(transport *http.Transport, rawProxyURL string, baseDialer *net.Dialer) error {
	return configureTransportProxy(transport, rawProxyURL, baseDialer, ProxyTransportObserver{})
}

// ConfigureTransportProxyWithObserver applies proxy settings and reports
// successful proxy TCP connections and SOCKS target-connect attempts.
func ConfigureTransportProxyWithObserver(
	transport *http.Transport,
	rawProxyURL string,
	baseDialer *net.Dialer,
	observer ProxyTransportObserver,
) error {
	return configureTransportProxy(transport, rawProxyURL, baseDialer, observer)
}

func configureTransportProxy(
	transport *http.Transport,
	rawProxyURL string,
	baseDialer *net.Dialer,
	observer ProxyTransportObserver,
) error {
	if transport == nil || strings.TrimSpace(rawProxyURL) == "" {
		return nil
	}
	if baseDialer == nil {
		baseDialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	}

	u, err := security.ParseProxyURL(rawProxyURL)
	if err != nil {
		return fmt.Errorf("parse proxy url: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "http", "https":
		var proxyDialer proxyConnectDialer = baseDialer
		if observer.OnProxyConnect != nil {
			proxyDialer = &observingProxyDialer{
				base:      baseDialer,
				onConnect: observer.OnProxyConnect,
			}
		}
		transport.Proxy = http.ProxyURL(u)
		transport.DialContext = proxyDialer.DialContext
		return nil
	case "socks5", "socks5h":
		var proxyDialer proxyConnectDialer = baseDialer
		if observer.OnProxyConnect != nil || observer.OnSOCKSTargetConnectStart != nil {
			var wrapConn func(net.Conn) net.Conn
			if observer.OnSOCKSTargetConnectStart != nil {
				wrapConn = func(conn net.Conn) net.Conn {
					return &observingSOCKSConn{
						Conn:                 conn,
						onTargetConnectStart: observer.OnSOCKSTargetConnectStart,
					}
				}
			}
			proxyDialer = &observingProxyDialer{
				base:      baseDialer,
				onConnect: observer.OnProxyConnect,
				wrapConn:  wrapConn,
			}
		}
		var auth *xproxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &xproxy.Auth{User: u.User.Username(), Password: password}
		}

		dialer, err := xproxy.SOCKS5("tcp", u.Host, auth, proxyDialer)
		if err != nil {
			return fmt.Errorf("build socks5 dialer: %w", err)
		}
		if cd, ok := dialer.(contextDialer); ok {
			transport.DialContext = cd.DialContext
			transport.Proxy = nil
			return nil
		}

		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			type result struct {
				conn net.Conn
				err  error
			}
			done := make(chan result, 1)
			go func() {
				conn, err := dialer.Dial(network, address)
				done <- result{conn: conn, err: err}
			}()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case out := <-done:
				return out.conn, out.err
			}
		}
		transport.Proxy = nil
		return nil
	default:
		return fmt.Errorf("unsupported proxy scheme: %s", u.Scheme)
	}
}
