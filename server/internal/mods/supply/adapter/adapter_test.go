package adapter

// golden vector 契约测试：签名/解析/哨兵错误归一化的固定向量（CI 强制）。
// 向量由独立实现（Python hashlib/hmac，PHP 口径）预计算，改动签名逻辑必须同步更新。

import (
	"context"
	"io"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
				// 新版：回声 includes_inactive=true，返回下架商品（真实协议：title 为多语言对象）
				_, _ = w.Write([]byte(`{"ok":true,"total":2,"includes_inactive":true,"items":[
					{"id":1,"title":{"zh-CN":"A","en":"A-en"},"description":{"zh-CN":"描述A"},"content":{"zh-CN":"详情A"},"price_amount":"12.34","is_active":true,"images":["https://img/a.png"],"wholesale_prices":[{"min_quantity":1,"unit_price":"10.00"}],"skus":[{"id":11,"sku_code":"SKU1","price_amount":"12.34","stock_quantity":5,"is_active":true}]},
					{"id":2,"title":{"en":"B-only"},"price_amount":"0.50","is_active":false,"skus":[]}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"total":1,"includes_inactive":false,"items":[{"id":1,"title":{"zh-CN":"A"},"price_amount":"12.34","is_active":true}]}`))
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
	if list.Total != 2 || len(list.Items) != 2 || !list.IncludesInactive {
		t.Fatalf("include_inactive 列表错误: %+v", list)
	}
	if list.Items[0].Price != 1234 {
		t.Fatalf("元→分转换错误: got %d want 1234", list.Items[0].Price)
	}
	// 多语言提取：zh-CN 优先；content 优先于 description；拿货价取批发第一档；封面取 images[0]
	if list.Items[0].Name != "A" || list.Items[0].Description != "详情A" {
		t.Fatalf("多语言字段提取错误: %+v", list.Items[0])
	}
	if list.Items[0].FactoryPrice != 1000 {
		t.Fatalf("批发价拿货价错误: got %d want 1000", list.Items[0].FactoryPrice)
	}
	if list.Items[0].Cover != "https://img/a.png" {
		t.Fatalf("封面提取错误: %+v", list.Items[0])
	}
	// zh-CN 缺失回退任意语言
	if list.Items[1].Name != "B-only" {
		t.Fatalf("多语言回退错误: %+v", list.Items[1])
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

// ---- S1：增量能力（updated_after）与多语言提取 ----

func TestDujiaoIncrementalList(t *testing.T) {
	var gotAfter, gotIncludeInactive string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAfter = r.URL.Query().Get("updated_after")
		gotIncludeInactive = r.URL.Query().Get("include_inactive")
		_, _ = w.Write([]byte(`{"ok":true,"total":1,"includes_inactive":true,"items":[
			{"id":9,"title":{"zh-CN":"变更商品"},"price_amount":"5.00","is_active":false}]}`))
	}))
	defer srv.Close()

	a := &dujiaoAdapter{
		protocol: "dujiao_next",
		creds:    Credentials{APIKey: "k", APISecret: "s"},
		t:        newTransportWithClient(srv.URL, nil, nil, srv.Client()),
	}
	var aIfc Adapter = a
	il, ok := aIfc.(IncrementalLister)
	if !ok {
		t.Fatal("dujiao 适配器必须实现 IncrementalLister")
	}
	after := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	list, err := il.ListProductsAfter(context.Background(), 1, 50, after)
	if err != nil {
		t.Fatalf("ListProductsAfter: %v", err)
	}
	if gotAfter != after.Format(time.RFC3339) {
		t.Fatalf("updated_after 参数错误: %q", gotAfter)
	}
	if gotIncludeInactive != "true" {
		t.Fatalf("增量必须带 include_inactive=true（看到下架变更）: %q", gotIncludeInactive)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "变更商品" || list.Items[0].IsActive {
		t.Fatalf("增量解析错误: %+v", list.Items)
	}
}

func TestDujiaoNotIncrementalDrivers(t *testing.T) {
	// acg/zcard 驱动不实现 IncrementalLister（协议无 updated_after）→ 引擎回落全量
	var _ Adapter = &acgFakaAdapter{}
	var _ Adapter = &zCardAdapter{}
	if _, ok := Adapter(&acgFakaAdapter{}).(IncrementalLister); ok {
		t.Fatal("acg_faka 不应实现 IncrementalLister")
	}
	if _, ok := Adapter(&zCardAdapter{}).(IncrementalLister); ok {
		t.Fatal("zcard 不应实现 IncrementalLister")
	}
}

func TestLocalizedText(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"平文字符串透传", "hello ", "hello"},
		{"zh_CN 优先", map[string]any{"zh-CN": "中文", "en": "english"}, "中文"},
		{"zh_TW 次选", map[string]any{"zh-TW": "繁體", "en": "english"}, "繁體"},
		{"缺失回退任意", map[string]any{"ja": "日本語", "en": "english"}, "english"},
		{"空值跳过", map[string]any{"zh-CN": "  ", "fr": "français"}, "français"},
		{"nil 返回空", nil, ""},
		{"数值返回空", 42, ""},
	}
	for _, c := range cases {
		if got := LocalizedText(c.in); got != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// ---- S2：限流/WAF 识别（传输层）----

func TestTransport429RetriesThenRateLimited(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error_code":"too_many_requests"}`))
	}))
	defer srv.Close()
	// retryIntervals [0]（秒）→ 快速重试一次后放弃
	tr := newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())
	_, err := tr.do(context.Background(), "GET", "/x", nil, nil, nil)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("429 耗尽应包装 ErrRateLimited: %v", err)
	}
	if calls != 2 {
		t.Fatalf("应重试一次: %d", calls)
	}
}

func TestTransportWAFHTMLDetected(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte("<html><body>Access Denied. Cloudflare</body></html>")) // 200 但 HTML
	}))
	defer srv.Close()
	tr := newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())
	_, err := tr.do(context.Background(), "GET", "/x", nil, nil, nil)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("WAF HTML 应归类 ErrRateLimited: %v", err)
	}
	if calls != 2 {
		t.Fatalf("非 JSON 应可重试: %d", calls)
	}
}

func TestTransportBusiness4xxNoRetry(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error_code":"not_found"}`))
	}))
	defer srv.Close()
	tr := newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())
	_, err := tr.do(context.Background(), "GET", "/x", nil, nil, nil)
	if err == nil || errors.Is(err, ErrRateLimited) {
		t.Fatalf("404 应是普通业务错误: %v", err)
	}
	if calls != 1 {
		t.Fatalf("业务 4xx 不应重试: %d", calls)
	}
}

func TestLooksLikeJSON(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`{"ok":true}`, true},
		{`  [1,2]`, true},
		{"", true},  // 空体放行
		{"<html>", false},
		{"\n\r {\"a\":1}", true},
		{"Access Denied", false},
	}
	for _, c := range cases {
		if got := looksLikeJSON([]byte(c.in)); got != c.want {
			t.Fatalf("looksLikeJSON(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

func TestDujiaoCreateOrderHTTPStatusErrors(t *testing.T) {
	// dujiao-next 实测契约：业务拒绝以 HTTP 状态 + body error_code 返回
	// （402 余额 / 409 库存 / 400 商品无效 / 200+payment_failed）——
	// 必须归一化哨兵而非当网络错误无限重试。body 里的 TraceID 标识驱动分支。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var mark struct {
			TraceID string `json:"trace_id"`
		}
		_ = json.Unmarshal(body, &mark)
		switch mark.TraceID {
		case "b402":
			w.WriteHeader(402)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":"insufficient_balance"}`))
		case "b409":
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":"insufficient_stock"}`))
		case "b400":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":"sku_unavailable"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":false,"status":"canceled","error_code":"payment_failed"}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	cases := []struct {
		traceID string
		want    error
	}{
		{"", ErrInsufficientBalance}, // default → HTTP 200 + payment_failed
		{"b402", ErrInsufficientBalance},
		{"b409", ErrNoStock},
		{"b400", ErrProductUnavailable},
	}
	for _, c := range cases {
		a := &dujiaoAdapter{
			protocol: "dujiao_next",
			creds:    Credentials{APIKey: "k", APISecret: "s"},
			t:        newTransportWithClient(srv.URL, nil, nil, srv.Client()),
		}
		if _, err := a.CreateOrder(ctx, CreateOrderReq{ProductCode: "11", Quantity: 1, DownstreamOrderNo: "x", TraceID: c.traceID}); err != c.want {
			t.Fatalf("traceID=%q 应归一化 %v, got %v", c.traceID, c.want, err)
		}
	}
}

func TestAcgFakaFixes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/shared/commodity/stock":
			// PHP getItemStock(): string → {"stock":"990"}（字符串）
			_, _ = w.Write([]byte(`{"code":200,"data":{"stock":"990"}}`))
		case "/shared/commodity/query":
			// 手动发货占位文案（status=1 但 secret 是占位）+ 真实卡混合
			_, _ = w.Write([]byte(`{"code":200,"data":{"status":1,"secret":"正在发货中，请耐心等待，如有疑问，请联系客服。\nCARD-REAL-1"}}`))
		case "/shared/commodity/trade":
			if r.PostForm.Get("request_no") == "dup" {
				_, _ = w.Write([]byte(`{"code":0,"msg":"The request ID already exists"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"tradeNo":"123","amount":"1.00","secret":"很抱歉，有人在你付款之前抢走了商品，请联系客服。"}}`))
		}
	}))
	defer srv.Close()
	a := &acgFakaAdapter{
		protocol: "acg_faka",
		creds:    Credentials{AppID: "1", AppKey: "K"},
		t:        newTransportWithClient(srv.URL, nil, nil, srv.Client()),
	}
	ctx := context.Background()

	// 1) stock 字符串解析
	stock, err := a.GetStock(ctx, "C1", "")
	if err != nil || stock != 990 {
		t.Fatalf("stock 字符串解析失败: stock=%d err=%v", stock, err)
	}
	// 2) 占位文案过滤：混合时只留真实卡 → delivered
	d, err := a.GetOrder(ctx, "T1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "delivered" || len(d.Cards) != 1 || d.Cards[0] != "CARD-REAL-1" {
		t.Fatalf("占位过滤错误: %+v", d)
	}
	// 3) 纯占位（trade 抢购失败文案）→ pending（不交付）
	tr, err := a.CreateOrder(ctx, CreateOrderReq{ProductCode: "C1", Quantity: 1, DownstreamOrderNo: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != "pending" || len(tr.Cards) != 0 {
		t.Fatalf("纯占位应 pending 无卡: %+v", tr)
	}
	// 4) 防重键冲突 → 哨兵
	if _, err := a.CreateOrder(ctx, CreateOrderReq{ProductCode: "C1", Quantity: 1, DownstreamOrderNo: "dup"}); err != ErrDuplicateSubmit {
		t.Fatalf("防重冲突应 ErrDuplicateSubmit, got %v", err)
	}
}
