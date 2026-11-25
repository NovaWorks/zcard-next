package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestACGCatalogStockAvoidsInventoryHydrationAndKeepsPurchaseReads(t *testing.T) {
	for _, shared := range []int{0, 1} {
		for _, stock := range []string{`773`, `0`, `"42"`, `null`, `-1`, `1.5`, `2147483648`} {
			t.Run(fmt.Sprintf("shared%d-stock%s", shared, stock), func(t *testing.T) {
				inventory, stockReads := 0, 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/shared/commodity/items":
						fmt.Fprintf(w, `{"code":200,"data":[{"id":1,"children":[{"code":"P","price":1,"stock":%s,"shared_sync":%d,"config":[]}]}]}`, stock, shared)
					case "/shared/commodity/inventory":
						inventory++
						fmt.Fprint(w, `{"code":200,"data":{"count":12,"config":[]}}`)
					case "/shared/commodity/stock":
						stockReads++
						fmt.Fprint(w, `{"code":200,"data":{"stock":0}}`)
					default:
						w.WriteHeader(404)
					}
				}))
				defer srv.Close()
				a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
				ctx := WithACGCatalogStock(context.Background())
				list, err := a.ListProducts(ctx, 1, 50, true)
				if err != nil || len(list.Items) != 1 {
					t.Fatalf("catalog=%+v err=%v", list, err)
				}
				valid := stock == `773` || stock == `0` || stock == `"42"`
				if list.Items[0].StockFromCatalog != valid || (inventory == 0) != valid {
					t.Fatalf("stock=%s trusted=%v inventory reads=%d", stock, list.Items[0].StockFromCatalog, inventory)
				}
				// Even the same context must never substitute catalog stock for a
				// direct product/SKU availability check used before purchasing.
				if n, err := a.GetStock(ctx, "P", "美国|"); err != nil || n != 0 || stockReads != 1 {
					t.Fatalf("purchase stock reused catalog: stock=%d reads=%d err=%v", n, stockReads, err)
				}
				before := inventory
				list, err = a.ListProducts(context.Background(), 1, 50, true)
				if err != nil || list.Items[0].StockFromCatalog || inventory != before+1 {
					t.Fatal("strict/ordinary catalog resolution skipped its inventory check", err)
				}
			})
		}
	}
}

func TestACGCustomerInventoryConfigExamples(t *testing.T) {
	for _, config := range []string{`[]`, `{}`, `{"category":{"规格A":"2.89"}}`} {
		t.Run(config, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/shared/commodity/inventory":
					fmt.Fprintf(w, `{"code":200,"data":{"count":773,"price":4.8,"config":%s}}`, config)
				case "/shared/commodity/valuation":
					fmt.Fprint(w, `{"code":200,"data":{"price":"2.89"}}`)
				default:
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			p, err := a.QuoteProduct(context.Background(), &Product{ID: "P", Price: 480, IsActive: true})
			if err != nil || p.FactoryPrice != 289 {
				t.Fatalf("customer config quote=%+v err=%v", p, err)
			}
			if strings.Contains(config, "category") && (len(p.SKUs) != 1 || p.SKUs[0].ID != "规格A|") {
				t.Fatal("object config lost race SKU", p.SKUs)
			}
		})
	}
	encoded, _ := json.Marshal(Product{Stock: 773, StockFromCatalog: true})
	var decoded Product
	_ = json.Unmarshal(encoded, &decoded)
	if decoded.StockFromCatalog || strings.Contains(string(encoded), "StockFromCatalog") {
		t.Fatal("maintenance-only provenance escaped into persisted/imported snapshots")
	}
}

func TestACGReadNoticesAllowSuffixButKeepFailuresDistinct(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    error
	}{
		{"当前商品已停售，请刷新后重试", ErrProductUnavailable},
		{"商品已下架！请稍后查看", ErrProductUnavailable},
		{"该商品已停止销售。", ErrProductUnavailable},
		{"商品不存在，请联系站长", ErrProductDeleted},
		{"该商品已删除: P", ErrProductDeleted},
		{"商品未下架", nil},
		{"商品已下架检测失败", nil},
		{"查询商品已停售状态失败", nil},
		{"库存不足", nil},
		{"商品已下架，操作过于频繁", ErrRateLimited},
	} {
		t.Run(tc.message, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"code": 500, "msg": tc.message})
			_, err := parseAcgReadResp(raw)
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("want %v got %v", tc.want, err)
			}
			if tc.want == nil && (err == nil || errors.Is(err, ErrProductUnavailable) || errors.Is(err, ErrProductDeleted)) {
				t.Fatalf("ambiguous failure became an ordinary skip: %v", err)
			}
			_, purchase := parseResp(raw)
			if errors.Is(purchase, ErrProductUnavailable) || errors.Is(purchase, ErrProductDeleted) {
				t.Fatal("read-only normalization changed purchase handling")
			}
		})
	}
	_, err := parseAcgReadResp([]byte(`{"code":403,"msg":"商品已下架，请检查权限"}`))
	if !ClassifyImportError(err).Pause || errors.Is(err, ErrProductUnavailable) {
		t.Fatal("authentication failure became an ordinary skip", err)
	}
}

func TestACGLegacyManualAndDraftQuotesSkipBeforeValuation(t *testing.T) {
	for _, flags := range []string{`"delivery_way":1,"draft_status":0`, `"delivery_way":0,"draft_status":1`} {
		t.Run(flags, func(t *testing.T) {
			valuations := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/shared/commodity/inventory" {
					fmt.Fprintf(w, `{"code":200,"data":{%s,"config":[]}}`, flags)
				} else if r.URL.Path == "/shared/commodity/valuation" {
					valuations++
					fmt.Fprint(w, `{"code":200,"data":{"price":"2.89"}}`)
				} else {
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			_, err := a.QuoteProduct(context.Background(), &Product{ID: "P", IsActive: true})
			if !errors.Is(err, ErrNotSupported) || valuations != 0 {
				t.Fatalf("unsupported quote err=%v valuation requests=%d", err, valuations)
			}
		})
	}
}
