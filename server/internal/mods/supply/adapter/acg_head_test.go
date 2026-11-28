package adapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAcgBrokenHeadPreservesItemAndContinuesCatalog(t *testing.T) {
	for _, realThrottle := range []bool{false, true} {
		t.Run(fmt.Sprint(realThrottle), func(t *testing.T) {
			var codes []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/shared/commodity/items" {
					fmt.Fprint(w, `{"code":200,"data":[{"id":1,"children":[{"id":101,"code":"A","price":1},{"id":102,"code":"B","price":1}]}]}`)
					return
				}
				r.ParseForm()
				code := r.Form.Get("sharedCode")
				codes = append(codes, code)
				if code == "A" {
					if realThrottle {
						fmt.Fprint(w, `{"code":429,"msg":"请求过于频繁"}`)
					} else {
						fmt.Fprint(w, `{"code":0,"msg":"服务器频繁请联系客服支持"}`)
					}
					return
				}
				fmt.Fprint(w, `{"code":200,"data":{"count":10,"config":"","draft_status":0}}`)
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			list, err := a.ListProducts(context.Background(), 1, 20, false)
			if realThrottle {
				if !errors.Is(err, ErrRateLimited) || len(codes) != 1 {
					t.Fatal("global throttle ignored", codes, err)
				}
				return
			}
			if err != nil || len(codes) != 2 || codes[1] != "B" || len(list.Items) != 1 || list.Items[0].ID != "B" || len(list.SkippedIDs) != 1 || list.SkippedIDs[0] != "A" || list.Total != 2 {
				t.Fatal("bad head blocked catalog or became an empty-spec product", list, codes, err)
			}
			if _, err := a.ResolveImportProducts(context.Background(), []string{"A"}); err == nil {
				t.Fatal("formal import accepted unreadable specs")
			}
		})
	}
}

func TestAcgExplicitRetryablePurchaseRejection(t *testing.T) {
	for _, body := range []string{`{"code":0,"msg":"订单未完成，请检查库存、规格或余额后再试"}`, `{"code":0,"msg":"unknown failure"}`, `{"code":200,"data":{}}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/shared/commodity/item" {
				w.WriteHeader(404)
				return
			}
			fmt.Fprint(w, body)
		}))
		a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
		_, err := a.CreateOrder(context.Background(), CreateOrderReq{ProductCode: "P", Quantity: 1})
		if body == `{"code":0,"msg":"订单未完成，请检查库存、规格或余额后再试"}` {
			if !errors.Is(err, ErrPurchaseRetryable) {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrPurchaseUncertain) {
			t.Fatal("ambiguous response can be repurchased", err)
		}
		srv.Close()
	}
}

func TestAcgRepeatedAmbiguousFrequencyStillStopsChannel(t *testing.T) {
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shared/commodity/items" {
			fmt.Fprint(w, `{"code":200,"data":[{"id":1,"children":[{"code":"A"},{"code":"B"},{"code":"C"},{"code":"D"}]}]}`)
			return
		}
		reads++
		fmt.Fprint(w, `{"code":0,"msg":"服务器频繁请联系客服支持"}`)
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	if _, err := a.ListProducts(context.Background(), 1, 20, false); !errors.Is(err, ErrRateLimited) || reads != 3 {
		t.Fatal("repeated channel-level frequency was ignored", reads, err)
	}
}
