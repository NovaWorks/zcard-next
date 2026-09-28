package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestZCardPingUnknownAndZeroBalance(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int64
	}{
		{`{"ok":true}`, -1}, // Older servers may skip authentication entirely.
		{`{"ok":true,"balance":-1}`, -1},
		{`{"ok":true,"currency":"CNY"}`, 0}, // Known zero omitted by JSON encoder.
		{`{"ok":true,"balance":0}`, 0},
		{`{"ok":true,"balance":12345,"currency":"CNY"}`, 12345},
	} {
		t.Run(tc.body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			a := &zCardAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			res, err := a.Ping(context.Background())
			if err != nil || res.Balance != tc.want {
				t.Fatalf("balance: %+v err=%v", res, err)
			}
		})
	}
}
