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

func TestCatalogDoesNotTreatMalformedSuccessAsEmpty(t *testing.T) {
	for _, driver := range []string{"zcard", "dujiao_next", "acg_faka"} {
		t.Run(driver, func(t *testing.T) {
			body := `{"ok":true}`
			if driver == "acg_faka" {
				body = `{"code":200,"data":null}`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			transport := newTransportWithClient(srv.URL, nil, nil, srv.Client())
			var a Adapter
			switch driver {
			case "zcard":
				a = &zCardAdapter{protocol: driver, t: transport}
			case "dujiao_next":
				a = &dujiaoAdapter{protocol: driver, t: transport}
			case "acg_faka":
				a = &acgFakaAdapter{protocol: driver, t: transport}
			}
			if _, err := a.ListProducts(context.Background(), 1, 50, true); err == nil {
				t.Fatal("incomplete response was accepted as an empty catalog")
			}
			if driver != "acg_faka" {
				body = `{"ok":true,"total":0}`
				if _, err := a.ListProducts(context.Background(), 1, 50, true); err == nil {
					t.Fatal("missing items accepted as empty catalog")
				}
			}
			if driver == "dujiao_next" {
				body = `{"ok":false,"total":0,"items":[]}`
				if _, err := a.ListProducts(context.Background(), 1, 50, true); err == nil {
					t.Fatal("failed response was accepted as an empty catalog")
				}
			}
			body = `{"ok":true,"total":0,"items":[]}`
			if driver == "acg_faka" {
				body = `{"code":200,"data":[]}`
			}
			empty, err := a.ListProducts(context.Background(), 1, 50, true)
			if err != nil || empty.Total != 0 || len(empty.Items) != 0 || empty.HasMore {
				t.Fatalf("valid empty catalog rejected: %+v, %v", empty, err)
			}
		})
	}
}
