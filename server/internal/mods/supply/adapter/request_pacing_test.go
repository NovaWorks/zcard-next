package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestMaintenancePacingCoversInventoryAndEverySKUAcrossProducts(t *testing.T) {
	var times []time.Time
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		if r.URL.Path == "/shared/commodity/inventory" {
			fmt.Fprint(w, `{"code":200,"data":{"config":"[category]\nA=1\nB=2"}}`)
		} else if r.URL.Path == "/shared/commodity/valuation" {
			fmt.Fprint(w, `{"code":200,"data":{"price":"1.00"}}`)
		} else {
			w.WriteHeader(404) // Market's optional modern-item probe may use this.
		}
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	ctx := WithRequestInterval(context.Background(), 160*time.Millisecond)
	for _, code := range []string{"P", "Q"} {
		if _, err := a.QuoteProduct(ctx, &Product{ID: code, Price: 100, IsActive: true}); err != nil {
			t.Fatal(err)
		}
	}
	if len(times) < 6 {
		t.Fatal("missing quote requests")
	}
	for i := 1; i < len(times); i++ {
		if delta := times[i].Sub(times[i-1]); delta < 135*time.Millisecond {
			t.Fatalf("maintenance requests not spaced: %s", delta)
		}
	}
}

func TestMaintenancePacingConcurrentWaits(t *testing.T) {
	ctx, cancel := context.WithTimeout(WithRequestInterval(context.Background(), 20*time.Millisecond), 2*time.Second)
	defer cancel()
	started := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := waitRequestInterval(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if time.Since(started) < 55*time.Millisecond {
		t.Fatal("concurrent reads bypassed the maintenance interval")
	}
}

func TestMaintenancePacingCancellationDoesNotSendAnotherRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(WithRequestInterval(context.Background(), time.Hour))
	if err := waitRequestInterval(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	started := time.Now()
	if err := waitRequestInterval(ctx); err != context.Canceled || time.Since(started) > time.Second {
		t.Fatalf("canceled wait: %v", err)
	}
	// A normal purchase context has no maintenance limiter attached.
	if err := waitRequestInterval(context.Background()); err != nil {
		t.Fatal(err)
	}
}
