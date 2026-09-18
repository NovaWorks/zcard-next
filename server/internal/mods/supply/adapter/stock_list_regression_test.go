package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecheckAcgMissingListStockIsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":200,"data":[{"id":1,"name":"test","children":[{"code":"A","name":"A","price":10,"delivery_way":0}]}]}`))
	}))
	defer srv.Close()
	a := &acgFakaAdapter{protocol: "acg_faka", creds: Credentials{AppID: "test", AppKey: "test"}, t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	list, err := a.PreviewProducts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatal("expected 1 product")
	}
	t.Logf("missing list stock normalized to %d", list.Items[0].Stock)
	if list.Items[0].Stock != -2 {
		t.Fatal("missing stock is incorrectly normalized to unlimited")
	}
}
