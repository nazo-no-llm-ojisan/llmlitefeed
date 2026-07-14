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
	"sync"
	"time"
)

const (
	DefaultTimeout  = 10 * time.Second
	MaxResponseSize = 10 * 1024 * 1024 // 10 MiB
)

// Resolver は名前解決を抽象化する。
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Fetcher は SSRF セーフな HTTP GET を提供する。
type Fetcher struct {
	client   *http.Client
	resolver func(ctx context.Context, network, host string) ([]netip.Addr, error)
	timeout  time.Duration

	mu     sync.Mutex
	ipAddr string // DialContext で接続する検証済み IP:port
}

// New は production 用の Fetcher を返す。
func New() *Fetcher {
	resolver := &net.Resolver{}
	f := &Fetcher{resolver: resolver.LookupNetIP, timeout: DefaultTimeout}
	transport := &http.Transport{
		DialContext: f.dialValidated,
	}
	f.client = &http.Client{
		Transport: transport,
		Timeout:   DefaultTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return f
}

// NewForTest は resolver と RoundTripper を差し替える。
func NewForTest(rt http.RoundTripper, resolver func(ctx context.Context, network, host string) ([]netip.Addr, error)) *Fetcher {
	return NewForTestWithTimeout(rt, resolver, DefaultTimeout)
}

// NewForTestWithTimeout は timeout も差し替える。
func NewForTestWithTimeout(rt http.RoundTripper, resolver func(ctx context.Context, network, host string) ([]netip.Addr, error), timeout time.Duration) *Fetcher {
	client := &http.Client{
		Transport: rt,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Fetcher{client: client, resolver: resolver, timeout: timeout}
}

// dialValidated は Fetch で検証済みの IP:port へ dial する。
// 呼び出しは Fetch が resolver で検証した addr を 1 回だけ保存し、RoundTrip 内 dialer がそれを使う。
func (f *Fetcher) dialValidated(ctx context.Context, network, _ string) (net.Conn, error) {
	f.mu.Lock()
	addr := f.ipAddr
	f.mu.Unlock()
	if addr == "" {
		return nil, errors.New("internal: no validated address")
	}
	d := net.Dialer{Timeout: f.timeout}
	return d.DialContext(ctx, network, addr)
}

// Fetch は URL を取得して body を返す。
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (string, error) {
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

	// 名前解決前の IP リテラル検査
	if ip, err := netip.ParseAddr(host); err == nil {
		if err := checkIP(ip); err != nil {
			return "", err
		}
	}

	// 名前解決して全 IP 検査
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

	// 検証済み IP を保存し、Transport の DialContext はそれを必ず使う
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

	f.mu.Lock()
	f.ipAddr = addr
	f.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	// req.URL.Host は元ホスト名のまま。TLS SNI に使われる。
	req.Host = host

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	// 3xx: リダイレクトを再 Fetch（毎回 SSRF 検査をくぐり抜ける）
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc == "" {
			return "", fmt.Errorf("redirect %d with empty Location", resp.StatusCode)
		}
		ref, err := url.Parse(loc)
		if err != nil {
			return "", fmt.Errorf("parse redirect location: %w", err)
		}
		next := *u
		if ref.IsAbs() {
			next = *ref
		} else {
			next.Path = ref.Path
			if ref.RawQuery != "" {
				next.RawQuery = ref.RawQuery
			}
		}
		return f.Fetch(ctx, next.String())
	}

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

// checkIP は IP が SSRF ブロック対象かを判定する。
func checkIP(ip netip.Addr) error {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return fmt.Errorf("blocked ip: %s", ip)
	}
	// IPv4-mapped IPv6 も検査
	if ip.Is4In6() {
		v4 := ip.Unmap()
		if v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
			return fmt.Errorf("blocked ip: %s", ip)
		}
	}
	return nil
}
