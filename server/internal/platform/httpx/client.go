package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrResponseTooLarge = errors.New("httpx: response body exceeds limit")

type Options struct {
	Timeout          time.Duration
	MaxResponseBytes int64
	RequireHTTPS     bool
	// Only this exact literal-loopback origin may use HTTP/private addressing.
	// Production callers leave it empty. Redirects cannot inherit the exemption.
	TestOrigin string
}
type ipResolver func(context.Context, string) ([]net.IPAddr, error)
type safeTransport struct {
	base          *http.Transport
	options       Options
	lookup        ipResolver
	legacyPrivate bool
}

func NewClient(options Options) (*http.Client, error) { return newClient(options, false) }
func newClient(o Options, legacy bool) (*http.Client, error) {
	if o.Timeout <= 0 {
		o.Timeout = 20 * time.Second
	}
	if o.MaxResponseBytes <= 0 {
		o.MaxResponseBytes = 8 << 20
	}
	if o.TestOrigin != "" {
		u, e := url.Parse(o.TestOrigin)
		if e != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Port() == "" || !net.ParseIP(u.Hostname()).IsLoopback() {
			return nil, fmt.Errorf("httpx: test origin must be exact literal loopback host and port")
		}
	}
	s := &safeTransport{options: o, lookup: net.DefaultResolver.LookupIPAddr, legacyPrivate: legacy}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	s.base = &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxIdleConns: 16, IdleConnTimeout: 30 * time.Second}
	s.base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return s.dial(ctx, network, addr, dialer.DialContext)
	}
	c := &http.Client{Transport: s, Timeout: o.Timeout}
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("httpx: redirect limit")
		}
		if err := s.checkURL(r.URL); err != nil {
			return err
		}
		r.Header.Del("Referer")
		if len(via) > 0 && origin(r.URL) != origin(via[0].URL) {
			r.Header.Del("Authorization")
			r.Header.Del("Cookie")
			r.Header.Del("Proxy-Authorization")
		}
		// Keep direct CheckRedirect callers safe as well as the actual dial path.
		_, err := s.addresses(r.Context(), r.URL.Hostname(), r.URL.Port(), origin(r.URL) == o.TestOrigin)
		return err
	}
	return c, nil
}
func origin(u *url.URL) string { return u.Scheme + "://" + u.Host }
func (s *safeTransport) checkURL(u *url.URL) error {
	if u == nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("httpx: invalid HTTP URL")
	}
	if s.options.RequireHTTPS && u.Scheme != "https" && origin(u) != s.options.TestOrigin {
		return fmt.Errorf("httpx: HTTPS required")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && IsPrivateIP(ip) && origin(u) != s.options.TestOrigin && !s.legacyPrivate {
		return &ErrBlockedAddress{Host: u.Hostname()}
	}
	return nil
}
func (s *safeTransport) addresses(ctx context.Context, host, port string, test bool) ([]net.IPAddr, error) {
	var ips []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		limited, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var err error
		ips, err = s.lookup(limited, host)
		if err != nil {
			return nil, fmt.Errorf("httpx: DNS lookup failed: %w", err)
		}
	}
	if len(ips) == 0 {
		return nil, &ErrBlockedAddress{Host: host}
	}
	for _, ip := range ips {
		if ip.Zone != "" || (IsPrivateIP(ip.IP) && !test && !s.legacyPrivate) {
			return nil, &ErrBlockedAddress{Host: host}
		}
	}
	return ips, nil
}
func (s *safeTransport) dial(ctx context.Context, network, addr string, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	test := false
	if s.options.TestOrigin != "" {
		u, _ := url.Parse(s.options.TestOrigin)
		test = u.Host == net.JoinHostPort(host, port)
	}
	ips, err := s.addresses(ctx, host, port, test)
	if err != nil {
		return nil, err
	}
	var last error
	for _, ip := range ips {
		// Pass the checked literal IP to the dialer; it must never resolve host again.
		conn, e := dial(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	return nil, last
}
func (s *safeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := s.checkURL(r.URL); err != nil {
		return nil, err
	}
	resp, err := s.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > s.options.MaxResponseBytes {
		resp.Body.Close()
		return nil, ErrResponseTooLarge
	}
	// Transport has already decoded gzip, so this also bounds decompressed data.
	resp.Body = &boundedBody{ReadCloser: resp.Body, remaining: s.options.MaxResponseBytes}
	return resp, nil
}
func (s *safeTransport) CloseIdleConnections() { s.base.CloseIdleConnections() }

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var one [1]byte
		n, err := b.ReadCloser.Read(one[:])
		if n > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}

// IsSameOrigin deliberately includes scheme and port; credentials must not
// spread to CDN redirects, subdomains or a differently configured market.
func IsSameOrigin(a, b string) bool {
	u, e := url.Parse(a)
	if e != nil {
		return false
	}
	v, e := url.Parse(b)
	return e == nil && strings.EqualFold(origin(u), origin(v))
}
