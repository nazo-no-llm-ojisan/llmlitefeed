package fetcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeResolver は呼び出しカウンタ付きで名前解決を偽装する。
type fakeResolver struct {
	mu     atomic.Int32
	result []netip.Addr
	err    error
}

func (f *fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	f.mu.Add(1)
	return f.result, f.err
}

// fakeRT はリクエストと Host→レスポンスのマッピングで偽のHTTP応答を返す。
type fakeRT struct {
	mu    atomic.Int32
	hosts map[string]*http.Response
	err   error
	seen  map[string]int
}

func (r *fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Add(1)
	if r.seen == nil {
		r.seen = map[string]int{}
	}
	// Fetcher は req.URL.Host をリテラルIPにせず req.Host にホスト名を残すので
	// req.Host を見る
	key := req.Host
	if key == "" {
		key = req.URL.Host
	}
	r.seen[key]++
	if r.err != nil {
		return nil, r.err
	}
	if resp, ok := r.hosts[key]; ok {
		return resp, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     http.Header{},
	}, nil
}

func okResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
}

func errResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
	}
}

// ---- 正常系 ----

func TestFetch_public_https_success(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	rt := &fakeRT{hosts: map[string]*http.Response{
		"example.com": okResponse("hello"),
	}}
	f := NewForTest(rt, resolver.LookupNetIP)

	got, err := f.Fetch(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if got != "hello" {
		t.Errorf("body = %q, want %q", got, "hello")
	}
}

// ---- スキーム ----

func TestFetch_rejects_non_http_scheme(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	rt := &fakeRT{hosts: map[string]*http.Response{}}
	f := NewForTest(rt, resolver.LookupNetIP)

	cases := []string{
		"file:///etc/passwd",
		"ftp://example.com/file",
		"gopher://example.com/",
		"javascript:alert(1)",
	}
	for _, url := range cases {
		_, err := f.Fetch(context.Background(), url)
		if err == nil {
			t.Errorf("Fetch(%q) should return error, got nil", url)
		}
	}
}

// ---- IPブラックリスト ----

func TestFetch_rejects_ipv4_loopback_private_linklocal_unspecified(t *testing.T) {
	cases := []string{
		"127.0.0.1",
		"127.1.2.3",
		"10.0.0.1",
		"172.16.0.1",
		"172.31.255.255",
		"192.168.1.1",
		"169.254.169.254",
		"0.0.0.0",
	}
	for _, ip := range cases {
		ipAddr := netip.MustParseAddr(ip)
		resolver := &fakeResolver{result: []netip.Addr{ipAddr}}
		rt := &fakeRT{hosts: map[string]*http.Response{}}
		f := NewForTest(rt, resolver.LookupNetIP)

		_, err := f.Fetch(context.Background(), "https://blocked.example.com/")
		if err == nil {
			t.Errorf("Fetch should reject %s, got nil error", ip)
		}
	}
}

func TestFetch_rejects_ipv6_loopback_private_linklocal_unspecified(t *testing.T) {
	cases := []string{
		"::1",
		"fe80::1",
		"fc00::1",
		"fd00::1",
		"::",
		"::ffff:127.0.0.1",
	}
	for _, ip := range cases {
		ipAddr := netip.MustParseAddr(ip)
		resolver := &fakeResolver{result: []netip.Addr{ipAddr}}
		rt := &fakeRT{hosts: map[string]*http.Response{}}
		f := NewForTest(rt, resolver.LookupNetIP)

		_, err := f.Fetch(context.Background(), "https://blocked.example.com/")
		if err == nil {
			t.Errorf("Fetch should reject %s, got nil error", ip)
		}
	}
}

func TestFetch_rejects_IP_literal_directly(t *testing.T) {
	rt := &fakeRT{hosts: map[string]*http.Response{}}
	resolver := &fakeResolver{result: nil}
	resolverFn := resolver.LookupNetIP
	f := NewForTest(rt, resolverFn)

	_, err := f.Fetch(context.Background(), "https://127.0.0.1/")
	if err == nil {
		t.Error("Fetch should reject IP literal 127.0.0.1, got nil error")
	}
}

// ---- 名前解決後の検査 ----

func TestFetch_rejects_when_any_resolved_IP_is_blocked(t *testing.T) {
	// 1つでも禁止IPが混じれば拒否
	resolver := &fakeResolver{result: []netip.Addr{
		netip.MustParseAddr("203.0.113.1"),
		netip.MustParseAddr("127.0.0.1"),
	}}
	rt := &fakeRT{hosts: map[string]*http.Response{}}
	f := NewForTest(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://mixed.example.com/")
	if err == nil {
		t.Error("Fetch should reject when any resolved IP is blocked")
	}
}

// ---- リダイレクト再検査 ----

func TestFetch_rejects_redirect_to_private_IP(t *testing.T) {
	// 1回目: 公開IPへ解決 → 302 redirect
	// 2回目: private IPへ解決 → 拒否
	callCount := atomic.Int32{}
	multiResolver := &multiResolver{
		results: [][]netip.Addr{
			{netip.MustParseAddr("203.0.113.1")},
			{netip.MustParseAddr("10.0.0.1")},
		},
		count: &callCount,
	}
	rt := &redirectRT{
		redirect: &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://private.example.com/"}},
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}
	f := NewForTest(rt, multiResolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://public.example.com/")
	if err == nil {
		t.Error("Fetch should reject redirect to private IP")
	}
	if callCount.Load() < 2 {
		t.Errorf("resolver should be called at least twice, got %d", callCount.Load())
	}
}

// ---- DNS rebinding 防止 ----

func TestFetch_does_not_re_resolve_after_validation(t *testing.T) {
	// 検査後の追加 DNS 解決が行われないこと（fake resolver の呼び出し回数が1）
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	rt := &fakeRT{hosts: map[string]*http.Response{
		"example.com": okResponse("ok"),
	}}
	f := NewForTest(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := resolver.mu.Load(); got != 1 {
		t.Errorf("resolver should be called exactly once, got %d", got)
	}
}

// ---- タイムアウト ----

func TestFetch_timeout(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	rt := &fakeRT{err: errors.New("context deadline exceeded")}
	f := NewForTestWithTimeout(rt, resolver.LookupNetIP, 50*time.Millisecond)

	_, err := f.Fetch(context.Background(), "https://example.com/")
	if err == nil {
		t.Error("Fetch should return timeout error")
	}
}

// ---- サイズ上限 ----

func TestFetch_rejects_response_over_10MiB(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	// 11 MiB の body
	bigBody := bytes.Repeat([]byte("a"), 11*1024*1024)
	rt := &fakeRT{hosts: map[string]*http.Response{
		"example.com": {
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(bigBody)),
			Header:     http.Header{},
		},
	}}
	f := NewForTest(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://example.com/")
	if err == nil {
		t.Error("Fetch should reject response over 10 MiB")
	}
}

// ---- 非2xx ----

func TestFetch_rejects_non_2xx(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	rt := &fakeRT{hosts: map[string]*http.Response{
		"example.com": errResponse(http.StatusNotFound),
	}}
	f := NewForTest(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://example.com/")
	if err == nil {
		t.Error("Fetch should reject 404")
	}
}

// ---- 補助型 ----

type multiResolver struct {
	mu      atomic.Int32
	results [][]netip.Addr
	count   *atomic.Int32
}

func (m *multiResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	i := m.mu.Add(1) - 1
	m.count.Add(1)
	if int(i) < len(m.results) {
		return m.results[i], nil
	}
	return nil, fmt.Errorf("resolver exhausted")
}

type redirectRT struct {
	redirect *http.Response
}

func (r *redirectRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.redirect != nil {
		resp := *r.redirect
		resp.Request = req
		return &resp, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     http.Header{},
		Request:    req,
	}, nil
}
