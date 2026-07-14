package fetcher

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- 共通 fake 部品 ----

type fakeResolver struct {
	mu     atomic.Int32
	result []netip.Addr
	err    error
}

func (f *fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	f.mu.Add(1)
	return f.result, f.err
}

type multiResolver struct {
	mu      atomic.Int32
	results [][]netip.Addr
}

func (m *multiResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	i := m.mu.Add(1) - 1
	if int(i) < len(m.results) {
		return m.results[i], nil
	}
	return nil, fmt.Errorf("resolver exhausted")
}

func (m *multiResolver) callCount() int32 { return m.mu.Load() }

type deterministicResolver struct {
	mu   sync.Mutex
	a, b netip.Addr
}

func (d *deterministicResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.HasPrefix(host, "a.") {
		return []netip.Addr{d.a}, nil
	}
	if strings.HasPrefix(host, "b.") {
		return []netip.Addr{d.b}, nil
	}
	return nil, fmt.Errorf("unexpected host: %s", host)
}

// ---- fake RoundTripper（http.Transport を介さず直接 response を返す）----

type bodyRecordingRT struct {
	closed    atomic.Int32
	status    int
	body      []byte
	header    http.Header
	respondTo func(req *http.Request) *http.Response
}

func (r *bodyRecordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.respondTo != nil {
		if resp := r.respondTo(req); resp != nil {
			return wrapResponse(resp, &r.closed), nil
		}
	}
	resp := &http.Response{
		StatusCode: r.status,
		Header:     r.header,
		Body:       io.NopCloser(bytes.NewReader(r.body)),
	}
	return wrapResponse(resp, &r.closed), nil
}

func wrapResponse(resp *http.Response, closed *atomic.Int32) *http.Response {
	if closed == nil {
		closed = &atomic.Int32{}
	}
	resp.Request = nil
	wrapped := &closeCountingBody{Reader: resp.Body, onClose: func() { closed.Add(1) }}
	resp.Body = wrapped
	return resp
}

type closeCountingBody struct {
	io.Reader
	onClose func()
	once    sync.Once
	closed  atomic.Int32
}

func (c *closeCountingBody) Close() error {
	c.once.Do(func() {
		c.closed.Add(1)
		c.onClose()
	})
	return nil
}

func (c *closeCountingBody) CloseCount() int32 { return c.closed.Load() }

type redirectRT struct {
	redirect    *http.Response
	final       *http.Response
	closed      *atomic.Int32 // redirect 元 body close カウンタ
	closedFinal *atomic.Int32 // final body close カウンタ
	calls       atomic.Int32
}

func (r *redirectRT) RoundTrip(req *http.Request) (*http.Response, error) {
	call := r.calls.Add(1)
	if r.redirect != nil && (r.final == nil || call == 1) {
		resp := *r.redirect
		resp.Request = req
		counter := r.closed
		if counter == nil {
			counter = &atomic.Int32{}
		}
		wrap := &closeCountingBody{Reader: resp.Body, onClose: func() {
			counter.Add(1)
		}}
		resp.Body = wrap
		return &resp, nil
	}
	if r.final != nil {
		counter := r.closedFinal
		if counter == nil {
			counter = &atomic.Int32{}
		}
		wrapped := &closeCountingBody{Reader: r.final.Body, onClose: func() {
			counter.Add(1)
		}}
		resp := *r.final
		resp.Body = wrapped
		return &resp, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

// ---- fake dial（実 http.Transport + DialContext 経路のテスト用）----

func newHTTPConn(response []byte) net.Conn {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		req, err := http.ReadRequest(bufio.NewReader(server))
		if err != nil {
			return
		}
		_ = req.Body.Close()
		_, _ = server.Write(response)
	}()
	return client
}

// recordingDial は DialContext 呼び出しを記録し、addr と返り値 conn を提供する。
type recordingDial struct {
	mu       sync.Mutex
	calls    []string
	conns    map[string]net.Conn
	default_ net.Conn
}

func (d *recordingDial) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.calls = append(d.calls, addr)
	var c net.Conn = d.default_
	if d.conns != nil {
		if v, ok := d.conns[addr]; ok {
			c = v
		}
	}
	d.mu.Unlock()
	return c, nil
}

func (d *recordingDial) Calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.calls))
	copy(out, d.calls)
	return out
}

func (d *recordingDial) CallCount() int { return len(d.Calls()) }

// blockingDial は context.Done() までブロックして timeout を発火させる。
type blockingDial struct{}

func (blockingDial) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// ---- RoundTripper 経路のテスト群 ----

func TestRoundTripperPath_public_https_success(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	rt := &bodyRecordingRT{status: 200, body: []byte("hello")}

	f := newWithRoundTripper(rt, resolver.LookupNetIP)

	got, err := f.Fetch(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if got != "hello" {
		t.Errorf("body = %q, want %q", got, "hello")
	}
	if rt.closed.Load() == 0 {
		t.Error("response body should be closed")
	}
}

func TestRoundTripperPath_rejects_non_http_scheme(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	rt := &bodyRecordingRT{status: 200, body: []byte("ok")}

	f := newWithRoundTripper(rt, resolver.LookupNetIP)

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

func TestRoundTripperPath_rejects_ipv4(t *testing.T) {
	cases := []string{
		"127.0.0.1", "127.1.2.3",
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "0.0.0.0",
	}
	for _, ip := range cases {
		ipAddr := netip.MustParseAddr(ip)
		resolver := &fakeResolver{result: []netip.Addr{ipAddr}}
		rt := &bodyRecordingRT{status: 200, body: []byte("ok")}

		f := newWithRoundTripper(rt, resolver.LookupNetIP)

		_, err := f.Fetch(context.Background(), "https://blocked.example.com/")
		if err == nil {
			t.Errorf("Fetch should reject %s, got nil error", ip)
		}
	}
}

func TestRoundTripperPath_rejects_ipv6(t *testing.T) {
	cases := []string{
		"::1", "fe80::1", "fc00::1", "fd00::1", "::", "::ffff:127.0.0.1",
	}
	for _, ip := range cases {
		ipAddr := netip.MustParseAddr(ip)
		resolver := &fakeResolver{result: []netip.Addr{ipAddr}}
		rt := &bodyRecordingRT{status: 200, body: []byte("ok")}

		f := newWithRoundTripper(rt, resolver.LookupNetIP)

		_, err := f.Fetch(context.Background(), "https://blocked.example.com/")
		if err == nil {
			t.Errorf("Fetch should reject %s, got nil error", ip)
		}
	}
}

func TestRoundTripperPath_rejects_IP_literal(t *testing.T) {
	resolver := &fakeResolver{result: nil}
	rt := &bodyRecordingRT{status: 200, body: []byte("ok")}

	f := newWithRoundTripper(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://127.0.0.1/")
	if err == nil {
		t.Error("Fetch should reject IP literal 127.0.0.1, got nil error")
	}
}

func TestRoundTripperPath_rejects_when_any_resolved_IP_blocked(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{
		netip.MustParseAddr("203.0.113.1"),
		netip.MustParseAddr("127.0.0.1"),
	}}
	rt := &bodyRecordingRT{status: 200, body: []byte("ok")}

	f := newWithRoundTripper(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://mixed.example.com/")
	if err == nil {
		t.Error("Fetch should reject when any resolved IP is blocked")
	}
}

func TestRoundTripperPath_rejects_redirect_to_private_IP(t *testing.T) {
	resolver := &multiResolver{
		results: [][]netip.Addr{
			{netip.MustParseAddr("203.0.113.1")},
			{netip.MustParseAddr("10.0.0.1")},
		},
	}
	rt := &redirectRT{
		redirect: &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://private.example.com/"}},
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}

	f := newWithRoundTripper(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://public.example.com/")
	if err == nil {
		t.Error("Fetch should reject redirect to private IP")
	}
	if resolver.callCount() < 2 {
		t.Errorf("resolver should be called at least twice, got %d", resolver.callCount())
	}
}

func TestRoundTripperPath_redirect_max_hops(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	rt := &redirectRT{
		redirect: &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"/next"}},
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}

	f := newWithRoundTripper(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://example.com/")
	if err == nil {
		t.Fatal("Fetch should error on too many redirects")
	}
	if !strings.Contains(err.Error(), "too many redirects") {
		t.Errorf("error = %v, want 'too many redirects'", err)
	}
}

func TestRoundTripperPath_rejects_response_over_10MiB(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	bigBody := bytes.Repeat([]byte("a"), 11*1024*1024)
	rt := &bodyRecordingRT{status: 200, body: bigBody}

	f := newWithRoundTripper(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://example.com/")
	if err == nil {
		t.Error("Fetch should reject response over 10 MiB")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want 'exceeds'", err)
	}
}

func TestRoundTripperPath_rejects_non_2xx(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	rt := &bodyRecordingRT{status: http.StatusNotFound, body: []byte("")}

	f := newWithRoundTripper(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://example.com/")
	if err == nil {
		t.Error("Fetch should reject 404")
	}
	if !strings.Contains(err.Error(), "non-2xx") {
		t.Errorf("error = %v, want 'non-2xx'", err)
	}
}

func TestRoundTripperPath_closes_body_on_redirect(t *testing.T) {
	resolver := &multiResolver{
		results: [][]netip.Addr{
			{netip.MustParseAddr("203.0.113.1")},
			{netip.MustParseAddr("203.0.113.2")},
		},
	}

	closed := &atomic.Int32{}
	closedFinal := &atomic.Int32{}
	resp := &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{"https://final.example.com/"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	rt := &redirectRT{redirect: resp, final: okResponse("done"), closed: closed, closedFinal: closedFinal}

	f := newWithRoundTripper(rt, resolver.LookupNetIP)

	_, err := f.Fetch(context.Background(), "https://start.example.com/")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if closed.Load() == 0 {
		t.Error("redirect response body should be closed before next fetch")
	}
	if closedFinal.Load() == 0 {
		t.Error("final response body should be closed after read")
	}
}

// ---- Dial 経路のテスト群 ----

// httpResp は最小限の HTTP/1.1 レスポンスを bytes にエンコードする。
func httpResp(status int, body string) []byte {
	statusLine := fmt.Sprintf("HTTP/1.1 %d Status\r\n", status)
	h := fmt.Sprintf("Content-Length: %d\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\n", len(body))
	return []byte(statusLine + h + body)
}

func TestDialPath_addr_passed_to_dial(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	dial := &recordingDial{
		conns: map[string]net.Conn{
			"203.0.113.1:80": newHTTPConn(httpResp(200, "hello")),
		},
	}

	f := newWithDial(dial.Dial, resolver.LookupNetIP, 0)

	got, err := f.Fetch(context.Background(), "http://example.com/")
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if got != "hello" {
		t.Errorf("body = %q, want %q", got, "hello")
	}
	calls := dial.Calls()
	if len(calls) != 1 {
		t.Fatalf("dial calls = %d, want 1", len(calls))
	}
	if calls[0] != "203.0.113.1:80" {
		t.Errorf("dial addr = %q, want 203.0.113.1:80", calls[0])
	}
	if resolver.mu.Load() != 1 {
		t.Errorf("resolver should be called exactly once, got %d", resolver.mu.Load())
	}
}

func TestDialPath_https_uses_validated_addr_and_original_sni(t *testing.T) {
	sni := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			sni <- hello.ServerName
			return nil, nil
		},
	}
	server.StartTLS()
	defer server.Close()

	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	var dialed atomic.Value
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed.Store(addr)
		var d net.Dialer
		return d.DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	f := newWithDial(dial, resolver.LookupNetIP, time.Second)

	_, err := f.Fetch(context.Background(), "https://example.com/")
	if err == nil {
		t.Fatal("Fetch should reject the untrusted test certificate")
	}
	if got, _ := dialed.Load().(string); got != "203.0.113.1:443" {
		t.Errorf("dial addr = %q, want 203.0.113.1:443", got)
	}
	if got := <-sni; got != "example.com" {
		t.Errorf("TLS SNI = %q, want example.com", got)
	}
}

func TestDialPath_timeout_via_context_deadline(t *testing.T) {
	resolver := &fakeResolver{result: []netip.Addr{netip.MustParseAddr("203.0.113.1")}}
	dial := blockingDial{}

	f := newWithDial(dial.Dial, resolver.LookupNetIP, 50*time.Millisecond)

	start := time.Now()
	_, err := f.Fetch(context.Background(), "https://example.com/")
	elapsed := time.Since(start)
	if err == nil {
		t.Error("Fetch should return timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Fetch took too long: %v", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestDialPath_concurrent_does_not_mix_ips(t *testing.T) {
	a := netip.MustParseAddr("203.0.113.10")
	b := netip.MustParseAddr("203.0.113.20")
	resolver := &deterministicResolver{a: a, b: b}
	dial := &recordingDial{
		conns: map[string]net.Conn{
			"203.0.113.10:80": newHTTPConn(httpResp(200, "ok-a")),
			"203.0.113.20:80": newHTTPConn(httpResp(200, "ok-b")),
		},
	}

	f := newWithDial(dial.Dial, resolver.LookupNetIP, 0)

	var wg sync.WaitGroup
	type result struct {
		body string
		err  error
	}
	results := make(chan result, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		body, err := f.Fetch(context.Background(), "http://a.example.com/")
		results <- result{body: body, err: err}
	}()
	go func() {
		defer wg.Done()
		body, err := f.Fetch(context.Background(), "http://b.example.com/")
		results <- result{body: body, err: err}
	}()
	wg.Wait()
	close(results)

	bodies := map[string]int{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("Fetch returned error: %v", result.err)
		}
		bodies[result.body]++
	}
	if bodies["ok-a"] != 1 || bodies["ok-b"] != 1 {
		t.Errorf("response bodies = %+v, want one each of ok-a and ok-b", bodies)
	}

	calls := dial.Calls()
	if len(calls) != 2 {
		t.Fatalf("dial calls = %d, want 2", len(calls))
	}
	gotAddrs := map[string]int{}
	for _, c := range calls {
		gotAddrs[c]++
	}
	if gotAddrs["203.0.113.10:80"] != 1 || gotAddrs["203.0.113.20:80"] != 1 {
		t.Errorf("expected exactly 1 call to each resolved IP, got %+v", gotAddrs)
	}
}

func okResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
}
