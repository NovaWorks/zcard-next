package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogReadRetriesWithoutConfiguredIntervals(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	tr := newTransportWithClient(server.URL, nil, nil, server.Client())
	ctx, cancel := context.WithTimeout(WithCatalogRead(context.Background()), 5*time.Second)
	defer cancel()
	if _, err := tr.do(ctx, "POST", "/items", nil, nil, nil); err == nil || calls.Load() != 2 {
		t.Fatalf("catalog must retry once even without configured intervals: calls=%d err=%v", calls.Load(), err)
	}
}

func TestCatalogReadHasIndependentTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Millisecond
	tr := newTransportWithClient(server.URL, []int{1}, nil, client)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e := tr.tryOnce(WithCatalogRead(ctx), "POST", server.URL, nil, nil); e != nil {
		t.Fatal("catalog kept short timeout", e)
	}
	if client.Timeout != 5*time.Millisecond {
		t.Fatal("shared client modified")
	}
	if _, e := tr.tryOnce(ctx, "POST", server.URL, nil, nil); e == nil {
		t.Fatal("normal requests inherited catalog timeout")
	}
}
func TestCatalogReadHonorsParentCancellationAndAuthFailure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(403) }))
	defer server.Close()
	tr := newTransportWithClient(server.URL, []int{1, 1, 1}, nil, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := tr.do(WithCatalogRead(ctx), "POST", "/items", nil, nil, nil); e == nil {
		t.Fatal("ignored cancellation")
	}
	if _, e := tr.do(WithCatalogRead(context.Background()), "POST", "/items", nil, nil, nil); e == nil || calls != 1 {
		t.Fatal("auth error retried", calls, e)
	}
}
