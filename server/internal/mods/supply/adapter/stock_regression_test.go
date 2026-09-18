package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuditAcgLegacyStockFallback(t *testing.T) {
	itemCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shared/commodity/stock":
			http.NotFound(w, r)
		case "/shared/commodity/item":
			itemCalls++
			w.Write([]byte(`{"code":200,"data":{"stock":"37"}}`))
		}
	}))
	defer srv.Close()
	a := &acgFakaAdapter{protocol: "acg_faka", creds: Credentials{AppID: "test", AppKey: "test"}, t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	n, err := a.GetStock(context.Background(), "A", "")
	t.Logf("legacy lookup: stock=%d err=%v fallback_calls=%d", n, err, itemCalls)
	if err != nil || n != 37 {
		t.Fatal("compatible item endpoint is never tried")
	}
}
func TestAuditAcgMissingStockIsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"code":200,"data":{}}`)) }))
	defer srv.Close()
	a := &acgFakaAdapter{protocol: "acg_faka", creds: Credentials{AppID: "test", AppKey: "test"}, t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	n, err := a.GetStock(context.Background(), "A", "")
	t.Logf("missing field: stock=%d err=%v", n, err)
	if err == nil && n >= -1 {
		t.Fatal("missing stock field is incorrectly accepted as zero")
	}
}
