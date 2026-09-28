package httpx

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDialPinsValidatedAddress(t *testing.T) {
	c, _ := NewClient(Options{})
	s := c.Transport.(*safeTransport)
	lookups := 0
	s.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		if lookups > 1 {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	sentinel := errors.New("no network")
	_, err := s.dial(context.Background(), "tcp", "attacker.example:443", func(_ context.Context, _ string, addr string) (net.Conn, error) {
		if addr != "8.8.8.8:443" {
			t.Fatalf("DNS was not pinned: %s", addr)
		}
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) || lookups != 1 {
		t.Fatalf("%v lookups=%d", err, lookups)
	}
	called := false
	_, err = s.dial(context.Background(), "tcp", "attacker.example:443", func(context.Context, string, string) (net.Conn, error) { called = true; return nil, sentinel })
	if err == nil || called {
		t.Fatal("rebound address was dialed")
	}
	s.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.0.0.1")}}, nil
	}
	_, err = s.dial(context.Background(), "tcp", "mixed.example:443", func(context.Context, string, string) (net.Conn, error) { called = true; return nil, sentinel })
	if err == nil || called {
		t.Fatal("mixed private DNS accepted")
	}
}
func TestMarketOriginAndDecodedBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		g := gzip.NewWriter(w)
		g.Write([]byte(strings.Repeat("x", 1024)))
		g.Close()
	}))
	defer srv.Close()
	c, err := NewClient(Options{TestOrigin: srv.URL, RequireHTTPS: true, MaxResponseBytes: 64, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("gzip limit: %v", err)
	}
	if _, err = c.Get(strings.Replace(srv.URL, "http:", "https:", 1)); err == nil {
		t.Fatal("test exemption crossed scheme")
	}
	strict, _ := NewClient(Options{RequireHTTPS: true})
	if _, err = strict.Get(srv.URL); err == nil {
		t.Fatal("strict client allowed loopback")
	}
	if _, err = NewClient(Options{TestOrigin: "http://localhost:8080"}); err == nil {
		t.Fatal("test origin must be literal loopback")
	}
}
func TestRedirectCredentialsAndPrivateTargets(t *testing.T) {
	c, _ := NewClient(Options{RequireHTTPS: true})
	s := c.Transport.(*safeTransport)
	s.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	old, _ := http.NewRequest("GET", "https://market.example/a", nil)
	next, _ := http.NewRequest("GET", "https://cdn.market.example/b", nil)
	for _, h := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer"} {
		next.Header.Set(h, "secret")
	}
	if err := c.CheckRedirect(next, []*http.Request{old}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer"} {
		if next.Header.Get(h) != "" {
			t.Fatal("redirect leaked", h)
		}
	}
	next, _ = http.NewRequest("GET", "https://169.254.169.254/metadata", nil)
	if c.CheckRedirect(next, []*http.Request{old}) == nil {
		t.Fatal("private redirect accepted")
	}
	next, _ = http.NewRequest("GET", "http://market.example/a", nil)
	if c.CheckRedirect(next, []*http.Request{old}) == nil {
		t.Fatal("TLS downgrade accepted")
	}
}

func TestOriginalTLSNameAndTimeout(t *testing.T) {
	var sni string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sni = r.TLS.ServerName; w.Write([]byte("ok")) }))
	defer server.Close()
	c, _ := NewClient(Options{RequireHTTPS: true, Timeout: time.Second})
	defer c.CloseIdleConnections()
	s := c.Transport.(*safeTransport)
	s.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	dialer := &net.Dialer{}
	s.base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return s.dial(ctx, network, addr, func(ctx context.Context, network, pinned string) (net.Conn, error) {
			if !strings.HasPrefix(pinned, "8.8.8.8:") {
				t.Fatal(pinned)
			}
			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		})
	}
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	s.base.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	// httptest's certificate is valid for example.com. Hostname verification stays on.
	response, e := c.Get("https://example.com/")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if sni != "example.com" {
		t.Fatal("lost original SNI", sni)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer slow.Close()
	limited, _ := NewClient(Options{TestOrigin: slow.URL, RequireHTTPS: true, Timeout: 30 * time.Millisecond})
	defer limited.CloseIdleConnections()
	response, e = limited.Get(slow.URL)
	if e == nil {
		_, e = io.ReadAll(response.Body)
		response.Body.Close()
	}
	if e == nil {
		t.Fatal("body timeout not enforced")
	}
}
