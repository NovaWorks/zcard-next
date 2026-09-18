package adapter

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestStockParsingAndLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int32
		fail       bool
	}{
		{"integer", `{"stock":37}`, 37, false}, {"string", `{"stock":"37"}`, 37, false},
		{"zero", `{"stock":0}`, 0, false}, {"unlimited", `{"stock":-1}`, -1, false},
		{"missing", `{}`, -2, true}, {"null", `{"stock":null}`, -2, true}, {"blank", `{"stock":""}`, -2, true},
		{"fraction", `{"stock":1.5}`, -2, true}, {"overflow", `{"stock":2147483648}`, -2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"code":200,"data":%s}`, tc.body) }))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			n, e := a.GetStock(context.Background(), "A", "")
			if (e != nil) != tc.fail || n != tc.want {
				t.Fatalf("stock=%d err=%v", n, e)
			}
		})
	}
	for _, shape := range []string{"404", "html404", "base64"} {
		t.Run(shape, func(t *testing.T) {
			stockCalls, itemCalls := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/shared/commodity/stock" {
					stockCalls++
					switch shape {
					case "404":
						http.NotFound(w, r)
					case "html404":
						fmt.Fprint(w, "<html><h1>404 Not Found</h1></html>")
					case "base64":
						fmt.Fprintf(w, `<script>atob("%s")</script>`, base64.StdEncoding.EncodeToString([]byte("<h1>404 Not Found</h1>")))
					}
					return
				}
				itemCalls++
				_ = r.ParseForm()
				if r.Form.Get("code") != "A" || r.Form.Get("sign") != AcgFakaSign(map[string]string{"code": "A", "app_id": "id", "app_key": "key"}, "key") {
					t.Error("legacy signature/parameter changed")
				}
				fmt.Fprint(w, `{"code":200,"data":{"stock":37}}`)
			}))
			defer srv.Close()
			a := &acgFakaAdapter{creds: Credentials{AppID: "id", AppKey: "key"}, t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			for i := 0; i < 2; i++ {
				n, e := a.GetStock(context.Background(), "A", "")
				if e != nil || n != 37 {
					t.Fatal(n, e)
				}
			}
			if stockCalls != 1 || itemCalls != 2 {
				t.Fatal("route probe repeated", stockCalls, itemCalls)
			}
		})
	}
}

func TestStockFallbackDoesNotHideErrorsOrDropSKU(t *testing.T) {
	for _, status := range []int{200, 401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var fallbacks atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/shared/commodity/stock" {
					fallbacks.Add(1)
				}
				w.WriteHeader(status)
				fmt.Fprint(w, "<html>Cloudflare access denied</html>")
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := a.GetStock(ctx, "A", ""); err == nil {
				t.Fatal("error hidden")
			}
			if fallbacks.Load() != 0 {
				t.Fatal("unsafe fallback")
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shared/commodity/stock" {
			t.Error("SKU query fell back to aggregate stock")
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	if _, err := a.GetStock(context.Background(), "A", "1天|区域=微信区"); err == nil {
		t.Fatal("legacy endpoint silently dropped SKU")
	}
}

func TestStockReadsRetryWithinDisplayBudgetAndLegacyZCardZero(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	a := &zCardAdapter{t: newTransportWithClient(srv.URL, []int{30, 60, 300}, nil, srv.Client())}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n, err := a.GetStock(ctx, "A", "")
	if err != nil || n != 0 || count != 2 {
		t.Fatal("old protobuf zero/retry broken", n, err, count)
	}
}

func TestStockOldSkinAndManualInventory(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint(manual), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				switch r.URL.Path {
				case "/shared/commodity/stock":
					http.NotFound(w, r)
				case "/shared/commodity/item":
					if r.Form.Get("code") != "" {
						fmt.Fprint(w, `{"code":500,"msg":"商品代码不能为空"}`)
					} else {
						http.NotFound(w, r)
					}
				case "/shared/commodity/inventory":
					if r.Form.Get("sharedCode") != "A" {
						t.Error("wrong parameter")
					}
					way := 0
					if manual {
						way = 1
					}
					fmt.Fprintf(w, `{"code":200,"data":{"delivery_way":%d,"count":0}}`, way)
				}
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			n, err := a.GetStock(context.Background(), "A", "")
			if manual {
				if err == nil {
					t.Fatal("manual stock misread as zero")
				}
			} else if err != nil || n != 0 {
				t.Fatal(n, err)
			}
		})
	}
}
