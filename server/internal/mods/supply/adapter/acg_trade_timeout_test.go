package adapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcgTradeTimeoutNeverRepeatsMutation(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shared/commodity/item" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/shared/commodity/trade" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		calls.Add(1)
		// The provider could have accepted and charged before the response
		// deadline. There is deliberately no authoritative rejection receipt.
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	a := &acgFakaAdapter{creds: Credentials{AppID: "1", AppKey: "K"}, t: newTransportWithClient(srv.URL, []int{0, 0}, nil, srv.Client())}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := a.CreateOrder(ctx, CreateOrderReq{ProductCode: "P", Quantity: 1, DownstreamOrderNo: "ABCDEFGHIJKLMNOPQRS"})
	if !errors.Is(err, ErrPurchaseUncertain) || !errors.Is(ctx.Err(), context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("timed-out trade must stay uncertain with one mutation: calls=%d err=%v ctx=%v", calls.Load(), err, ctx.Err())
	}
}
