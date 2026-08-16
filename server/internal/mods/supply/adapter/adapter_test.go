package adapter

// golden vector 契约测试：签名/解析/哨兵错误归一化的固定向量（CI 强制）。
// 向量由独立实现（Python hashlib/hmac，PHP 口径）预计算，改动签名逻辑必须同步更新。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- 签名 golden vectors（与 Python 独立计算对照）----

func TestGoldenSignatures(t *testing.T) {
	const secret = "test-secret-0123456789abcdef"

	t.Run("zcard_4head_ping", func(t *testing.T) {
		got := ZCardSign(secret, "POST", "/api/supply/ping", "", "1700000000", "zc_1700000000_abc12345", nil)
		want := "9d6e52a8034afb38711e1aa17651de0335abcdbd42465882a7f3ddb749a42123"
		if got != want {
			t.Fatalf("ZCardSign 向量漂移: got %s want %s", got, want)
		}
	})

	t.Run("zcard_4head_order_body", func(t *testing.T) {
		body := []byte(`{"product_id":"1","quantity":1}`)
		got := ZCardSign(secret, "POST", "/api/supply/orders", "", "1700000001", "zc_1700000001_x1y2z3a4", body)
		want := "f62cf07f9ef9a4797e51f7c4a82f5bc9a30a7b8980592d7bae243f23a55cf10d"
		if got != want {
			t.Fatalf("ZCardSign(body) 向量漂移: got %s want %s", got, want)
		}
	})

	t.Run("dujiao_3head", func(t *testing.T) {
		got := DujiaoSign(secret, "GET", "/api/v1/upstream/products", "1700000000", nil)
		want := "0f62715533760637016e7d94edc67dc11d0c665bc28234348a2a2892e10c4340"
		if got != want {
			t.Fatalf("DujiaoSign 向量漂移: got %s want %s", got, want)
		}
	})

	t.Run("acgfaka_body_sign", func(t *testing.T) {
		params := map[string]string{
			"shared_code": "abc123", "num": "2", "contact": "trace-1",
			"request_no": "order-001", "card_id": "0",
			"app_id": "1001", "app_key": "k9f8a7b6c5",
		}
		got := AcgFakaSign(params, "k9f8a7b6c5")
		want := "c3eddd5736d0c60b0c543ee687df695f"
		if got != want {
			t.Fatalf("AcgFakaSign 向量漂移: got %s want %s", got, want)
		}
	})

	t.Run("acgfaka_ksort_and_empty_filter", func(t *testing.T) {
		// 空值必须被过滤（1.x array_filter 语义）；排序决定字节
		params := map[string]string{
			"b": "", "a": "1", "app_id": "1001", "app_key": "k",
		}
		got := AcgFakaSign(params, "k")
		// 期望串: a=1&app_id=1001&app_key=k&key=k（app_key 作为普通参数参与签名 + 末尾 key 段）
		want := md5Hex([]byte("a=1&app_id=1001&app_key=k&key=k"))
		if got != want {
			t.Fatalf("AcgFakaSign 空值过滤漂移: got %s want %s", got, want)
		}
	})
}

// ---- 响应解析（httptest 模拟上游；回环地址经 validateURL 测试注入放行）----

func TestZCardAdapterParsing(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Supply-Signature")
		switch r.URL.Path {
		case "/api/supply/ping":
			_, _ = w.Write([]byte(`{"ok":true,"name":"上游站","balance":12345,"currency":"CNY"}`))
		case "/api/supply/products":
			_, _ = w.Write([]byte(`{"items":[{"id":7,"name":"月卡","price":1000,"factory_price":500,"category_id":3,"is_active":true,"stock":99}],"total":1,"page_size":50}`))
		case "/api/supply/orders":
			_, _ = w.Write([]byte(`{"supply_order_id":42,"amount":1000,"fulfillment":{"status":"delivered","cards":["CARD-A","CARD-B"]}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	a := &zCardAdapter{
		protocol: "zcard",
		creds:    Credentials{APIKey: "k", APISecret: "s"},
		t:        newTransportWithClient(srv.URL, nil, nil, srv.Client()),
	}
	ctx := context.Background()

	ping, err := a.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if ping.Balance != 12345 || ping.SiteName != "上游站" {
		t.Fatalf("Ping 解析错误: %+v", ping)
	}
	if gotSig == "" {
		t.Fatal("缺少 X-Supply-Signature 头")
	}

	list, err := a.ListProducts(ctx, 1, 50, false)
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != "7" || list.Items[0].Price != 1000 {
		t.Fatalf("商品解析错误: %+v", list.Items)
	}
	if list.HasMore {
		t.Fatal("total=1 page_size=50 不应 has_more")
	}

	ord, err := a.CreateOrder(ctx, CreateOrderReq{ProductCode: "7", Quantity: 2, DownstreamOrderNo: "o-1"})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if ord.UpstreamOrderID != "42" || ord.Status != "delivered" || len(ord.Cards) != 2 {
		t.Fatalf("下单解析错误: %+v", ord)
	}
}

func TestDujiaoAdapterParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/upstream/products":
			if r.URL.Query().Get("include_inactive") == "true" {
				// 新版：回声 includes_inactive=true，返回下架商品
				_, _ = w.Write([]byte(`{"ok":true,"total":2,"includes_inactive":true,"items":[
					{"id":1,"name":"A","price_amount":"12.34","is_active":true,"skus":[{"id":11,"sku_code":"SKU1","price_amount":"12.34","stock_quantity":5,"is_active":true}]},
					{"id":2,"name":"B","price_amount":"0.50","is_active":false,"skus":[]}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"total":1,"includes_inactive":false,"items":[{"id":1,"name":"A","price_amount":"12.34","is_active":true}]}`))
		case "/api/v1/upstream/orders":
			_, _ = w.Write([]byte(`{"ok":false,"error_code":"insufficient_balance","error_message":"余额不足"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	a := &dujiaoAdapter{
		protocol: "dujiao_next",
		creds:    Credentials{APIKey: "k", APISecret: "s"},
		t:        newTransportWithClient(srv.URL, nil, nil, srv.Client()),
	}
	ctx := context.Background()

	list, err := a.ListProducts(ctx, 1, 50, true)
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if list.Total != 2 || len(list.Items) != 2 {
		t.Fatalf("include_inactive 列表错误: %+v", list)
	}
	if list.Items[0].Price != 1234 {
		t.Fatalf("元→分转换错误: got %d want 1234", list.Items[0].Price)
	}
	if list.Items[0].SKUs[0].Stock != 5 || !list.Items[0].SKUs[0].IsActive {
		t.Fatalf("SKU 解析错误: %+v", list.Items[0].SKUs)
	}
	if list.Items[1].IsActive {
		t.Fatal("下架商品 is_active 应为 false")
	}

	// 余额不足 → 哨兵归一化
	_, err = a.CreateOrder(ctx, CreateOrderReq{ProductCode: "11", Quantity: 1, DownstreamOrderNo: "o-2"})
	if err != ErrInsufficientBalance {
		t.Fatalf("余额不足应归一化为 ErrInsufficientBalance, got %v", err)
	}
}

func TestAcgFakaParsing(t *testing.T) {
	// PHP_EOL 拆卡边界 + status=1 才认发货 + 元→分
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			t.Errorf("acg-faka 必须表单提交, got %s", r.Header.Get("Content-Type"))
		}
		_ = r.ParseForm()
		if r.Form.Get("sign") == "" {
			t.Error("body 必须携带 sign")
		}
		switch r.URL.Path {
		case "/shared/commodity/items":
			_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":[{"id":5,"name":"分类A","children":[
				{"code":"P1","name":"商品1","price":"10.50","factory_price":"8.00","status":1,"stock":3},
				{"code":"P2","name":"商品2","price":"1.25","status":0}]}]}`))
		case "/shared/commodity/trade":
			_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{"tradeNo":"T-100","amount":"20.00","secret":"CARD-1\r\nCARD-2\nCARD-3"}}`))
		case "/shared/commodity/query":
			_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{"status":1,"secret":"CARD-9\n"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	a := &acgFakaAdapter{
		protocol: "acg_faka",
		creds:    Credentials{AppID: "1001", AppKey: "k9f8a7b6c5"},
		t:        newTransportWithClient(srv.URL, nil, nil, srv.Client()),
	}
	ctx := context.Background()

	list, err := a.ListProducts(ctx, 1, 50, false)
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("商品数错误: %d", len(list.Items))
	}
	if list.Items[0].Price != 1050 || list.Items[0].FactoryPrice != 800 || list.Items[0].Stock != 3 {
		t.Fatalf("acg 商品解析错误: %+v", list.Items[0])
	}
	if list.Items[0].CategoryID != "5" || list.Items[1].IsActive {
		t.Fatalf("分类/下架解析错误: %+v", list.Items[1])
	}

	ord, err := a.CreateOrder(ctx, CreateOrderReq{ProductCode: "P1", Quantity: 3, DownstreamOrderNo: "o-3", TraceID: "t1"})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	// PHP_EOL（\r\n）与 \n 混用必须拆出 3 张
	if len(ord.Cards) != 3 {
		t.Fatalf("PHP_EOL 拆卡错误: got %d cards %v", len(ord.Cards), ord.Cards)
	}
	if ord.UpstreamOrderID != "T-100" || ord.Amount != 2000 || ord.Status != "delivered" {
		t.Fatalf("下单解析错误: %+v", ord)
	}

	detail, err := a.GetOrder(ctx, "T-100")
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	// 尾部 \n 不应产生空卡
	if len(detail.Cards) != 1 || detail.Cards[0] != "CARD-9" || detail.Status != "delivered" {
		t.Fatalf("查单解析错误: %+v", detail)
	}

	// 退款不支持
	if err := a.RefundOrder(ctx, "T-100"); err != ErrNotSupported {
		t.Fatalf("acg 退款应 ErrNotSupported, got %v", err)
	}
}

// ---- SSRF / 构造校验 ----

func TestSSRFRejectsPrivateBaseURL(t *testing.T) {
	for _, driver := range []string{"zcard", "dujiao_next", "acg_faka"} {
		if _, err := New(driver, "http://127.0.0.1:8000", Credentials{}, nil); err == nil {
			t.Fatalf("%s: 内网 base_url 必须被拒", driver)
		}
		if _, err := New(driver, "http://192.168.1.1", Credentials{}, nil); err == nil {
			t.Fatalf("%s: 内网 base_url 必须被拒", driver)
		}
		if _, err := New(driver, "ftp://example.com", Credentials{}, nil); err == nil {
			t.Fatalf("%s: 非 http 协议必须被拒", driver)
		}
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New("unknown", "https://example.com", Credentials{}, nil); err == nil {
		t.Fatal("未知驱动必须报错")
	}
}

// ---- 工具函数覆盖 ----

func TestParseYuanToCents(t *testing.T) {
	cases := map[string]int64{"12.34": 1234, "0.50": 50, "10": 1000, "": 0, "abc": 0, "10.005": 1001}
	for in, want := range cases {
		if got := parseYuanToCents(in); got != want {
			t.Fatalf("parseYuanToCents(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSplitCards(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"A\r\nB\nC", 3},
		{"A\rB", 2},
		{"A\n\nB", 2}, // 空行过滤
		{"", 0},
		{"   ", 0},
	}
	for _, c := range cases {
		if got := splitCards(c.in); len(got) != c.want {
			t.Fatalf("splitCards(%q) = %d cards, want %d", c.in, len(got), c.want)
		}
	}
}

func TestIDString(t *testing.T) {
	var raw json.RawMessage
	_ = json.Unmarshal([]byte(`[7, "abc", 12.0, null]`), &raw)
	var arr []any
	_ = json.Unmarshal(raw, &arr)
	if got := idString(arr[0]); got != "7" {
		t.Fatalf("idString(number) = %q", got)
	}
	if got := idString(arr[1]); got != "abc" {
		t.Fatalf("idString(string) = %q", got)
	}
	if got := idString(arr[2]); got != "12" {
		t.Fatalf("idString(float) = %q", got)
	}
	if got := idString(arr[3]); got != "" {
		t.Fatalf("idString(null) = %q", got)
	}
}
