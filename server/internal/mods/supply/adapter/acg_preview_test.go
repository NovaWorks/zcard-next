package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestAcgLargeCatalogPreviewAndSelectedImport(t *testing.T) {
	products := make([]map[string]any, 3000)
	for i := range products {
		products[i] = map[string]any{
			"code": fmt.Sprintf("P%d", i), "name": "长效账号商品", "price": "9.90",
			"delivery_way": 0, "stock": "100",
		}
	}
	var inventoryCodes []string
	itemsCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shared/commodity/items":
			itemsCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []any{
				map[string]any{"id": 1, "name": "账号", "children": products},
			}})
		case "/shared/commodity/inventory":
			_ = r.ParseForm()
			code := r.PostForm.Get("sharedCode")
			inventoryCodes = append(inventoryCodes, code)
			draft := 0
			if code == "P2999" {
				draft = 1
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{
				"factory_price": "2.50", "draft_status": draft,
				"config": "[category]\n1天=1.00\n7天=5.00\n",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}

	preview, err := a.PreviewProducts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 3000 || preview.HasMore || itemsCalls != 1 || len(inventoryCodes) != 0 {
		t.Fatalf("大目录预览不应逐品查询: items=%d more=%v requests=%d inventory=%v", len(preview.Items), preview.HasMore, itemsCalls, inventoryCodes)
	}
	resolved, err := a.ResolveImportProducts(context.Background(), []string{"P2", "P2999", "deleted"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inventoryCodes, []string{"P2", "P2999"}) || len(resolved.Items) != 2 {
		t.Fatalf("只应补齐仍存在的勾选商品: inventory=%v products=%d", inventoryCodes, len(resolved.Items))
	}
	for _, p := range resolved.Items {
		if p.FactoryPrice != 250 || len(p.SKUs) != 2 || p.SKUs[1].Price != 500 {
			t.Fatalf("导入丢失拿货价或规格: %+v", p)
		}
		if p.IsActive != (p.ID == "P2") {
			t.Fatalf("导入未使用 inventory 的预选状态: %+v", p)
		}
	}
}

func TestAcgSelectedImportInventoryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shared/commodity/items" {
			_, _ = w.Write([]byte(`{"code":200,"data":[{"id":1,"children":[{"code":"P1","price":9.9}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":403,"msg":"禁止读取规格"}`))
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	if _, err := a.PreviewProducts(context.Background()); err != nil {
		t.Fatalf("预览不应依赖 inventory: %v", err)
	}
	if _, err := a.ResolveImportProducts(context.Background(), []string{"P1"}); err == nil {
		t.Fatal("规格查询失败不可按无规格商品导入")
	}
}

func TestAcgCatalogCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shared/commodity/items" {
			_, _ = w.Write([]byte(`{"code":200,"data":[{"id":1,"children":[{"code":"P1","price":9.9}]}]}`))
			return
		}
		cancel()
		_, _ = w.Write([]byte(`{"code":403}`))
	}))
	defer srv.Close()
	defer cancel()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	if _, err := a.ListProducts(ctx, 1, 50, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("不能将取消后的不完整商品缓存为成功: %v", err)
	}
}
