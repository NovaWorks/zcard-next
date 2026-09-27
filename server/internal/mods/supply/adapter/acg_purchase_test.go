package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// ACG does not replay a successful order for request_no. Repeating a trade
// after a lost response only hides the original failure behind a duplicate.
func TestAcgTradeDoesNotRetryUncertainResponse(t *testing.T) {
	for _, mode := range []string{"502", "429", "html", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/shared/commodity/trade" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if calls.Add(1) > 1 {
					_, _ = w.Write([]byte(`{"code":0,"msg":"The request ID already exists"}`))
					return
				}
				switch mode {
				case "502":
					w.WriteHeader(http.StatusBadGateway)
				case "429":
					w.WriteHeader(http.StatusTooManyRequests)
				case "html":
					_, _ = w.Write([]byte("<html>Gateway unavailable</html>"))
				case "disconnect":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
				}
			}))
			defer srv.Close()
			a := &acgFakaAdapter{creds: Credentials{AppID: "1", AppKey: "K"}, t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}
			_, err := a.CreateOrder(context.Background(), CreateOrderReq{ProductCode: "P", Quantity: 1, DownstreamOrderNo: "ABCDEFGHIJKLMNOPQRS"})
			if err == nil {
				t.Fatal("uncertain response accepted")
			}
			if calls.Load() != 1 || err == ErrDuplicateSubmit {
				t.Fatalf("trade repeated and obscured first response: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestAcgOrderQueryStillRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shared/commodity/query" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"status":1,"secret":"CARD"}}`))
	}))
	defer srv.Close()
	a := &acgFakaAdapter{creds: Credentials{AppID: "1", AppKey: "K"}, t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}
	res, err := a.GetOrder(context.Background(), "UP-1")
	if err != nil || calls.Load() != 2 || res.Status != "delivered" {
		t.Fatalf("read-only query retry broken: calls=%d result=%+v err=%v", calls.Load(), res, err)
	}
}
