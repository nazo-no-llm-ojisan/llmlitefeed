package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

const (
	DefaultTimeout  = 10 * time.Second
	MaxResponseSize = 10 * 1024 * 1024 // 10 MiB
	MaxRedirectHops = 10
)

// DialFunc は addr (host:port) で net.Conn を開く。addr は検証済み IP:port。
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Fetcher は SSRF セーフな HTTP GET を提供する。
type Fetcher struct {
	client      *http.Client
	resolver    func(ctx context.Context, network, host string) ([]netip.Addr, error)
	timeout     time.Duration
	dialContext DialFunc // テスト用 injection seam（非公開）
}

// New は production 用の Fetcher を返す。
func New() *Fetcher {
	resolver := &net.Resolver{}
	f := &Fetcher{resolver: resolver.LookupNetIP, timeout: DefaultTimeout}
	f.client = &http.Client{
		Timeout: DefaultTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return f
}

// newWithRoundTripper はテスト用 RoundTripper を持つ Fetcher を返す（internal）。
func newWithRoundTripper(rt http.RoundTripper, resolver func(ctx context.Context, network, host string) ([]netip.Addr, error)) *Fetcher {
	f := &Fetcher{resolver: resolver, timeout: DefaultTimeout}
	f.client = &http.Client{
		Transport: rt,
		Timeout:   DefaultTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return f
}

// newWithDial は Dial 関数を injection seam に差し込むテスト用 Fetcher（internal）。
func newWithDial(dial DialFunc, resolver func(ctx context.Context, network, host string) ([]netip.Addr, error), timeout time.Duration) *Fetcher {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Fetcher{
		resolver:    resolver,
		timeout:     timeout,
		dialContext: dial,
	}
}

// Fetch は URL を取得して body を返す。
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (string, error) {
	return f.fetchWithHops(ctx, rawURL, 0)
}

func (f *Fetcher) fetchWithHops(ctx context.Context, rawURL string, hops int) (string, error) {
	if hops >= MaxRedirectHops {
		return "", fmt.Errorf("too many redirects (%d)", hops)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", errors.New("empty host")
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if err := checkIP(ip); err != nil {
			return "", err
		}
	}

	rctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	addrs, err := f.resolver(rctx, "ip", host)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("no addresses for %q", host)
	}
	for _, a := range addrs {
		if err := checkIP(a); err != nil {
			return "", fmt.Errorf("blocked address for %q: %w", host, err)
		}
	}

	primary := addrs[0]
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(primary.String(), port)

	client := f.clientForRequest(addr)

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}

	// 3xx: リダイレクトを再 Fetch（毎回 SSRF 検査をくぐり抜ける）
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		// 次の Fetch に進む前に現在 resp.Body を必ず close する。
		// 標準 http.body の wrap 後に Close 観測が困難になるため、ここで明示 close する。
		_ = resp.Body.Close()
		if loc == "" {
			return "", fmt.Errorf("redirect %d with empty Location", resp.StatusCode)
		}
		ref, err := url.Parse(loc)
		if err != nil {
			return "", fmt.Errorf("parse redirect location: %w", err)
		}
		next := u.ResolveReference(ref)
		return f.fetchWithHops(ctx, next.String(), hops+1)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("non-2xx status: %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, MaxResponseSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if len(body) > MaxResponseSize {
		return "", fmt.Errorf("response body exceeds %d bytes", MaxResponseSize)
	}
	return string(body), nil
}

// clientForRequest は各リクエスト用に検証済み addr 専用の *http.Client を返す。
func (f *Fetcher) clientForRequest(addr string) *http.Client {
	timeout := f.timeout
	base := f.client
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if base != nil && base.Transport != nil {
		client.Transport = base.Transport
		return client
	}
	if f.dialContext != nil {
		dial := f.dialContext
		client.Transport = &http.Transport{
			DialContext: func(dctx context.Context, network, _ string) (net.Conn, error) {
				return dial(dctx, network, addr)
			},
		}
		return client
	}
	fixedAddr := addr
	client.Transport = &http.Transport{
		DialContext: func(dctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			return d.DialContext(dctx, network, fixedAddr)
		},
	}
	return client
}

// checkIP は IP が SSRF ブロック対象かを判定する。
func checkIP(ip netip.Addr) error {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return fmt.Errorf("blocked ip: %s", ip)
	}
	if ip.Is4In6() {
		v4 := ip.Unmap()
		if v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
			return fmt.Errorf("blocked ip: %s", ip)
		}
	}
	return nil
}
