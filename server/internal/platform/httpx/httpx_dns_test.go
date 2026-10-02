package httpx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func disallowPrivateForTest(t *testing.T) {
	t.Helper()
	old := allowPrivate
	allowPrivate = false
	t.Cleanup(func() { allowPrivate = old })
}

func TestSafeClientBlocksDNSRebindingBeforeRequest(t *testing.T) {
	disallowPrivateForTest(t)
	var requests, dials atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer srv.Close()
	lookups := 0
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		lookups++
		ip := "8.8.8.8"
		if lookups > 1 {
			ip = "127.0.0.1"
		}
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
	// The same check used by ValidateURL initially sees a public address.
	if _, err := resolveHost(context.Background(), "supplier.example", lookup); err != nil {
		t.Fatal(err)
	}
	client := NewSafeClient(time.Second)
	transport := client.Transport.(*http.Transport)
	defer transport.CloseIdleConnections()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return safeDialContext(ctx, network, addr, lookup, func(ctx context.Context, network, addr string) (net.Conn, error) {
			dials.Add(1)
			return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(srv.URL, "http://"))
		})
	}
	_, err := client.Get("http://supplier.example/img.png")
	var blocked *ErrBlockedAddress
	if !errors.As(err, &blocked) {
		t.Fatalf("connection DNS changed to loopback but was not blocked: %v", err)
	}
	if lookups != 2 || dials.Load() != 0 || requests.Load() != 0 {
		t.Fatalf("rebinding must be blocked before dialing or issuing HTTP: lookups=%d, dials=%d, requests=%d", lookups, dials.Load(), requests.Load())
	}
}

func TestSafeDialRejectsEntireMixedDNSAnswer(t *testing.T) {
	disallowPrivateForTest(t)
	for _, private := range []string{"10.0.0.1", "169.254.169.254", "::1", "::ffff:192.168.0.1"} {
		t.Run(private, func(t *testing.T) {
			dials := 0
			_, err := safeDialContext(context.Background(), "tcp", "supplier.example:443",
				func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP(private)}}, nil
				}, func(context.Context, string, string) (net.Conn, error) {
					dials++
					return nil, errors.New("unexpected dial")
				})
			var blocked *ErrBlockedAddress
			if !errors.As(err, &blocked) || dials != 0 {
				t.Fatalf("mixed answer must be rejected before its public member is dialed: err=%v, dials=%d", err, dials)
			}
		})
	}
}

func TestSafeDialPinsIPsAndFallsBackWithinTimeout(t *testing.T) {
	disallowPrivateForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	lookups := 0
	var addresses []string
	peer, expected := net.Pipe()
	defer peer.Close()
	defer expected.Close()
	conn, err := safeDialContext(ctx, "tcp4", "supplier.example:443",
		func(context.Context, string) ([]net.IPAddr, error) {
			lookups++
			return []net.IPAddr{{IP: net.ParseIP("2606:4700::1111")}, {IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}}, nil
		}, func(attemptCtx context.Context, network, addr string) (net.Conn, error) {
			addresses = append(addresses, addr)
			if network != "tcp4" {
				t.Fatalf("network changed: %s", network)
			}
			if len(addresses) == 1 {
				<-attemptCtx.Done() // The first address is unreachable.
				return nil, attemptCtx.Err()
			}
			return expected, nil
		})
	if err != nil || conn != expected || ctx.Err() != nil {
		t.Fatalf("second public address must be attempted before the overall timeout: %v", err)
	}
	if lookups != 1 || len(addresses) != 2 || addresses[0] != "8.8.8.8:443" || addresses[1] != "1.1.1.1:443" {
		t.Fatalf("dialing must use checked literal IPs without resolving again: lookups=%d, addresses=%v", lookups, addresses)
	}
}

func TestSafeDialUsesCallerContextAndPrivateOverride(t *testing.T) {
	disallowPrivateForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := safeDialContext(ctx, "tcp", "supplier.example:443",
		func(got context.Context, host string) ([]net.IPAddr, error) {
			if got != ctx {
				t.Fatal("resolver did not receive the caller context")
			}
			return nil, got.Err()
		}, func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("canceled lookup must not dial")
			return nil, nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("resolver cancellation was lost: %v", err)
	}

	allowPrivate = true
	peer, expected := net.Pipe()
	defer peer.Close()
	defer expected.Close()
	conn, err := safeDialContext(context.Background(), "tcp", "dev.example:9000",
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}, func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr != "127.0.0.1:9000" {
				t.Fatalf("private override must still pin the resolved address: %s", addr)
			}
			return expected, nil
		})
	if err != nil || conn != expected {
		t.Fatalf("explicit development override was not honored: %v", err)
	}
}

func TestSafeDialIPv6UnreachableFallsBackToIPv4(t *testing.T) {
	disallowPrivateForTest(t)
	peer, expected := net.Pipe()
	defer peer.Close()
	defer expected.Close()
	var addresses []string
	conn, err := safeDialContext(context.Background(), "tcp", "supplier.example:443",
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("2606:4700::1111")}, {IP: net.ParseIP("8.8.8.8")}}, nil
		}, func(ctx context.Context, network, addr string) (net.Conn, error) {
			addresses = append(addresses, addr)
			if len(addresses) == 1 {
				return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ENETUNREACH}
			}
			return expected, nil
		})
	if err != nil || conn != expected || len(addresses) != 2 || addresses[0] != "[2606:4700::1111]:443" || addresses[1] != "8.8.8.8:443" {
		t.Fatalf("unreachable IPv6 should immediately fall back to checked IPv4: err=%v, addresses=%v", err, addresses)
	}
}

func TestSafeDialPreservesTLSHostname(t *testing.T) {
	disallowPrivateForTest(t)
	serverName := make(chan string, 1)
	requestHost := make(chan string, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHost <- r.Host
	}))
	srv.TLS = &tls.Config{GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
		serverName <- info.ServerName
		return nil, nil
	}}
	srv.StartTLS()
	defer srv.Close()
	host := srv.Certificate().DNSNames[0]
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	client := NewSafeClient(time.Second)
	transport := client.Transport.(*http.Transport)
	defer transport.CloseIdleConnections()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return safeDialContext(ctx, network, addr, func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}, func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr != net.JoinHostPort("8.8.8.8", port) {
				return nil, fmt.Errorf("unexpected dial address: %s", addr)
			}
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		})
	}
	resp, err := client.Get("https://" + net.JoinHostPort(host, port) + "/img.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if got := <-serverName; got != host {
		t.Fatalf("TLS SNI changed to the pinned IP: %s", got)
	}
	if got := <-requestHost; got != net.JoinHostPort(host, port) {
		t.Fatalf("HTTP Host changed to the pinned IP: %s", got)
	}
}
