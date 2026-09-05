package supplier

// B/C 兼容层契约测试：
// - 签名向量与 supply/adapter 包跨实现一致（同一上游协议两处实现不漂移）
// - dujiao 兼容层端到端（ping/products/下单/查单/幂等/余额不足口径/取消/验签失败）
// - acg-faka 兼容层端到端（connect/items/trade secret/query/幂等/验签失败）

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	invport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
)

// ── fakes ──────────────────────────────────────────────────

type fakeCatalog struct{ prods []catalogport.SupplierProduct }

func (f *fakeCatalog) ListForSupply(_ context.Context, flt catalogport.AdminFilter) ([]catalogport.SupplierProduct, int64, error) {
	out := make([]catalogport.SupplierProduct, 0)
	for _, p := range f.prods {
		if flt.Status == 1 && p.Status != 1 {
			continue
		}
		out = append(out, p)
	}
	return out, int64(len(out)), nil
}
func (f *fakeCatalog) GetForSupply(_ context.Context, id uint64) (*catalogport.SupplierProduct, error) {
	for _, p := range f.prods {
		if p.ID == id {
			p := p
			return &p, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (f *fakeCatalog) ListSupplyCategories(context.Context) ([]catalogport.SupplyCategory, error) {
	return []catalogport.SupplyCategory{{ID: 7, Name: "数码"}}, nil
}

type fakeInv struct{}

func (f *fakeInv) Reserve(_ context.Context, _ uint64, items []invport.ReserveItem) (*invport.Reservation, error) {
	cards := make([]invport.ReservedCard, 0)
	for _, it := range items {
		for i := 0; i < int(it.Quantity); i++ {
			cards = append(cards, invport.ReservedCard{CardID: uint64(100 + len(cards))})
		}
	}
	return &invport.Reservation{Cards: cards}, nil
}
func (f *fakeInv) Release(context.Context, uint64) error { return nil }
func (f *fakeInv) BindOrder(context.Context, uint64, uint64, uint64, int32) error {
	return nil
}
func (f *fakeInv) MarkUsed(context.Context, []uint64, uint64) error { return nil }
func (f *fakeInv) Stock(_ context.Context, _ uint64, _ uint64) (int64, error) {
	return 5, nil
}

type fakeCards struct{}

func (f *fakeCards) Contents(_ context.Context, cardIDs []uint64, _, _ uint64) ([]string, error) {
	out := make([]string, 0, len(cardIDs))
	for _, id := range cardIDs {
		out = append(out, fmt.Sprintf("CARD-%d", id))
	}
	return out, nil
}

// newCompatEnv 兼容层测试环境（repo + 三 fake + 服务 + 两 mux）。
func newCompatEnv(t *testing.T) (*SupplyAPIService, *SupplierRepoImpl, *fakeCatalog) {
	t.Helper()
	repo, _ := newSupplierTestData(t)
	cat := &fakeCatalog{prods: []catalogport.SupplierProduct{
		{ID: 1, Name: "月卡", Price: 1000, FactoryPrice: 500, CategoryID: 7, Status: 1, Description: "描述"},
		{ID: 2, Name: "下架品", Price: 500, CategoryID: 7, Status: 0},
	}}
	svc := &SupplyAPIService{repo: repo, reader: cat, inv: &fakeInv{}, cards: &fakeCards{}}
	return svc, repo, cat
}

// seedCompatAccount 建指定协议的 approved 账号（充值余额分）。
func seedCompatAccount(t *testing.T, repo *SupplierRepoImpl, protocol, apiKey, secret string, balance int64) *ent.SupplierAccount {
	t.Helper()
	ctx := context.Background()
	acc, err := repo.CreateAccount(ctx, "下游-"+protocol, apiKey, secret, "", protocol, "测试店铺")
	if err != nil {
		t.Fatal(err)
	}
	acc, err = repo.ReviewAccount(ctx, acc.ID, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if balance > 0 {
		if err := repo.Recharge(ctx, acc.ID, balance, "recharge:test:"+apiKey, "测试"); err != nil {
			t.Fatal(err)
		}
	}
	return acc
}

// ── 签名向量（跨实现一致） ───────────────────────────────────

func TestCompatSignGoldenVectors(t *testing.T) {
	// dujiao：与 supply/adapter 包 DujiaoSign 的既有向量一致
	// （向量值来自 adapter_test.go TestGoldenSignatures/dujiao_3head，Python 预计算）
	got := dujiaoSign("test-secret-0123456789abcdef", "GET", "/api/v1/upstream/products", "1700000000", nil)
	want := "0f62715533760637016e7d94edc67dc11d0c665bc28234348a2a2892e10c4340"
	if got != want {
		t.Fatalf("dujiao 签名向量漂移: got %s", got)
	}

	// acg：与 supply/adapter 包 AcgFakaSign 的既有向量一致（含空值剔除与 ksort）
	params := map[string]string{
		"shared_code": "abc123", "num": "2", "contact": "trace-1",
		"request_no": "order-001", "card_id": "0",
		"app_id": "1001", "app_key": "k9f8a7b6c5",
	}
	wantAcg := "c3eddd5736d0c60b0c543ee687df695f"
	if !acgFakaSignVerify(params, "k9f8a7b6c5", wantAcg) {
		t.Fatal("acg 签名向量漂移")
	}
	// 空字符串值剔除、0/"0" 保留
	if !acgFakaSignVerify(map[string]string{"b": "", "a": "1", "app_id": "1001", "app_key": "k"}, "k", md5HexStr("a=1&app_id=1001&app_key=k&key=k")) {
		t.Fatal("空值剔除语义漂移")
	}
	// 错误签名拒绝
	if acgFakaSignVerify(params, "k9f8a7b6c5", "deadbeef") {
		t.Fatal("错误签名不应通过")
	}
}

// ── dujiao 兼容层端到端 ─────────────────────────────────────

func TestDujiaoCompatEndToEnd(t *testing.T) {
	svc, repo, _ := newCompatEnv(t)
	seedCompatAccount(t, repo, "dujiao_next", "dj-key-001", "dj-secret-xyz", 10000)
	srv := httptest.NewServer(dujiaoMux(svc))
	defer srv.Close()

	djDo := func(method, pathWithQuery string, body []byte) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+pathWithQuery, bytes.NewReader(body))
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		signPath, _, _ := strings.Cut(pathWithQuery, "?") // 签名串不含 query（协议口径）
		req.Header.Set("Dujiao-Next-Api-Key", "dj-key-001")
		req.Header.Set("Dujiao-Next-Timestamp", ts)
		req.Header.Set("Dujiao-Next-Signature", dujiaoSign("dj-secret-xyz", method, signPath, ts, body))
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return resp.StatusCode, m
	}

	t.Run("ping", func(t *testing.T) {
		st, m := djDo("POST", "/api/v1/upstream/ping", nil)
		if st != 200 || m["ok"] != true {
			t.Fatalf("ping: %d %v", st, m)
		}
		if m["balance"] != "100.00" || m["site_name"] != "测试店铺" {
			t.Fatalf("ping 字段口径错误: %v", m)
		}
	})

	t.Run("products_金额与SKU形态", func(t *testing.T) {
		st, m := djDo("GET", "/api/v1/upstream/products?include_inactive=true", nil)
		if st != 200 || m["ok"] != true {
			t.Fatalf("products: %d %v", st, m)
		}
		items := m["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("include_inactive 应含下架: %v", m)
		}
		p0 := items[0].(map[string]any)
		if p0["price_amount"] != "10.00" || p0["title"].(map[string]any)["zh-CN"] != "月卡" {
			t.Fatalf("商品字段口径错误: %v", p0)
		}
		if p0["id"].(float64) != 1 { // dujiao 客户端 uint（数字）
			t.Fatalf("id 应为数字: %v", p0["id"])
		}
		sku := p0["skus"].([]any)[0].(map[string]any)
		if sku["id"].(float64) != 1 || sku["stock_quantity"].(float64) != 5 {
			t.Fatalf("SKU 口径错误: %v", sku)
		}
	})

	t.Run("下单_交付_查单_幂等", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"sku_id": 1, "quantity": 2, "downstream_order_no": "dj-o-1"})
		st, m := djDo("POST", "/api/v1/upstream/orders", body)
		if st != 200 || m["ok"] != true || m["status"] != "delivered" || m["amount"] != "20.00" {
			t.Fatalf("下单: %d %v", st, m)
		}
		orderID := strconv.FormatUint(uint64(m["order_id"].(float64)), 10)
		// 幂等：重复下单返回首单
		st2, m2 := djDo("POST", "/api/v1/upstream/orders", body)
		if st2 != 200 || strconv.FormatUint(uint64(m2["order_id"].(float64)), 10) != orderID {
			t.Fatalf("幂等应返回首单: %d %v", st2, m2)
		}
		// 查单：fulfillment.payload = \n 连接卡密
		st3, m3 := djDo("GET", "/api/v1/upstream/orders/"+orderID, nil)
		if st3 != 200 {
			t.Fatalf("查单: %d", st3)
		}
		ff := m3["fulfillment"].(map[string]any)
		if ff["payload"] != "CARD-100\nCARD-101" {
			t.Fatalf("payload 口径错误: %v", ff["payload"])
		}
		// 已交付取消 → 409 cancel_not_allowed
		st4, m4 := djDo("POST", "/api/v1/upstream/orders/"+orderID+"/cancel", nil)
		if st4 != 409 || m4["error_code"] != "cancel_not_allowed" {
			t.Fatalf("已交付取消应 409: %d %v", st4, m4)
		}
	})

	t.Run("余额不足_200_payment_failed", func(t *testing.T) {
		seedCompatAccount(t, repo, "dujiao_next", "dj-key-poor", "dj-secret-poor", 0)
		body, _ := json.Marshal(map[string]any{"sku_id": 1, "quantity": 1, "downstream_order_no": "dj-o-poor"})
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/upstream/orders", bytes.NewReader(body))
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set("Dujiao-Next-Api-Key", "dj-key-poor")
		req.Header.Set("Dujiao-Next-Timestamp", ts)
		req.Header.Set("Dujiao-Next-Signature", dujiaoSign("dj-secret-poor", "POST", "/api/v1/upstream/orders", ts, body))
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if resp.StatusCode != 200 || m["ok"] != false || m["error_code"] != "payment_failed" {
			t.Fatalf("余额不足应为 200+payment_failed: %d %v", resp.StatusCode, m)
		}
	})

	t.Run("验签失败401", func(t *testing.T) {
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/upstream/ping", nil)
		req.Header.Set("Dujiao-Next-Api-Key", "dj-key-001")
		req.Header.Set("Dujiao-Next-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		req.Header.Set("Dujiao-Next-Signature", "bad")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("验签失败应 401: %d", resp.StatusCode)
		}
	})
}

// ── acg-faka 兼容层端到端 ───────────────────────────────────

func TestAcgCompatEndToEnd(t *testing.T) {
	svc, repo, _ := newCompatEnv(t)
	seedCompatAccount(t, repo, "acg_faka", "1001", "k9f8a7b6c5", 10000)
	srv := httptest.NewServer(acgMux(svc))
	defer srv.Close()

	acgDo := func(path string, extra map[string]string) (map[string]any, error) {
		t.Helper()
		form := url.Values{}
		for k, v := range extra {
			form.Set(k, v)
		}
		form.Set("app_id", "1001")
		form.Set("app_key", "k9f8a7b6c5")
		// 签名串：ksort + 剔除空串值（复用验签函数的口径构造 sign）
		form.Set("sign", acgTestSign(form, "k9f8a7b6c5"))
		resp, err := srv.Client().PostForm(srv.URL+path, form)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	}

	t.Run("connect", func(t *testing.T) {
		m, err := acgDo("/shared/authentication/connect", nil)
		if err != nil {
			t.Fatal(err)
		}
		if m["code"].(float64) != 200 {
			t.Fatalf("connect: %v", m)
		}
		data := m["data"].(map[string]any)
		if data["shopName"] != "测试店铺" || data["balance"].(float64) != 100 {
			t.Fatalf("connect 字段口径错误: %v", data)
		}
	})

	t.Run("items_两级树", func(t *testing.T) {
		m, _ := acgDo("/shared/commodity/items", nil)
		tree := m["data"].([]any)
		if len(tree) != 1 {
			t.Fatalf("分类树: %v", tree)
		}
		cat := tree[0].(map[string]any)
		if cat["name"] != "数码" {
			t.Fatalf("分类名: %v", cat)
		}
		children := cat["children"].([]any)
		p0 := children[0].(map[string]any)
		if p0["code"] != "1" || p0["price"] != "10.00" || p0["stock"].(float64) != 5 {
			t.Fatalf("商品字段口径错误: %v", p0)
		}
	})

	t.Run("trade_secret与幂等", func(t *testing.T) {
		m, err := acgDo("/shared/commodity/trade", map[string]string{
			"shared_code": "1", "num": "2", "contact": "c@x.com", "request_no": "req-001",
		})
		if err != nil {
			t.Fatal(err)
		}
		if m["code"].(float64) != 200 {
			t.Fatalf("trade: %v", m)
		}
		data := m["data"].(map[string]any)
		if data["secret"] != "CARD-100\nCARD-101" || data["amount"].(float64) != 20 {
			t.Fatalf("trade 字段口径错误: %v", data)
		}
		tradeNo := data["tradeNo"].(string)
		// 幂等：同 request_no 重放返回同单同卡密
		m2, _ := acgDo("/shared/commodity/trade", map[string]string{
			"shared_code": "1", "num": "2", "contact": "c@x.com", "request_no": "req-001",
		})
		d2 := m2["data"].(map[string]any)
		if d2["tradeNo"] != tradeNo || d2["secret"] != "CARD-100\nCARD-101" {
			t.Fatalf("幂等重放应返回同单同卡密: %v", d2)
		}
		// query
		mq, _ := acgDo("/shared/commodity/query", map[string]string{"tradeNo": tradeNo})
		qd := mq["data"].(map[string]any)
		if qd["status"].(float64) != 1 || qd["secret"] != "CARD-100\nCARD-101" {
			t.Fatalf("query 口径错误: %v", qd)
		}
	})

	t.Run("签名错误_code0", func(t *testing.T) {
		form := url.Values{}
		form.Set("app_id", "1001")
		form.Set("app_key", "k9f8a7b6c5")
		form.Set("sign", "wrong")
		resp, err := srv.Client().PostForm(srv.URL+"/shared/authentication/connect", form)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		if resp.StatusCode != 200 || m["code"].(float64) != 0 || m["msg"] != "密钥错误" {
			t.Fatalf("签名错误应 code:0: %d %v", resp.StatusCode, m)
		}
	})

	t.Run("库存估值", func(t *testing.T) {
		ms, _ := acgDo("/shared/commodity/stock", map[string]string{"code": "1"})
		if ms["data"].(map[string]any)["stock"].(float64) != 5 {
			t.Fatalf("stock: %v", ms)
		}
		mv, _ := acgDo("/shared/commodity/valuation", map[string]string{"code": "1", "num": "3"})
		if mv["data"].(map[string]any)["price"].(float64) != 30 {
			t.Fatalf("valuation: %v", mv)
		}
	})
}

// acgTestSign 测试侧签名构造（ksort + 空值剔除口径与 acgFakaSignVerify 对偶）。
func acgTestSign(form url.Values, key string) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		if form.Get(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	// 排序（含 sign 自身会被剔除——验签侧 k=="sign" 跳过）
	sorted := make([]string, 0, len(keys)+1)
	keys = append(keys, "sign")
	n := len(keys)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, k := range keys {
		if k == "sign" {
			continue
		}
		sorted = append(sorted, k+"="+form.Get(k))
	}
	return md5HexStr(strings.Join(sorted, "&") + "&key=" + key)
}

// md5HexStr MD5 十六进制小写（signing.go md5HexBytes 同源）。
func md5HexStr(s string) string {
	return md5HexBytes([]byte(s))
}
