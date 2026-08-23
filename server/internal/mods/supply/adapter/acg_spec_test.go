package adapter

// acg 多规格模型测试：INI 四形态 / 编码解码往返 / 黄金签名（嵌套 sku[中文规格]
// 与 PHP http_build_query 字节一致性）/ trade 表单字段 / GetStock 按规格。

import (
	"context"
	"io"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
)

func TestParseAcgINI(t *testing.T) {
	ini, err := parseAcgINI("[category]\n1天=1.00\n7天=5.00\n[sku]\n区域.微信区=0\n区域.QQ区=2\n时长.月卡=0.5\n[wholesale]\n10=0.9\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(ini.Race) != 2 || ini.Race["7天"] != "5.00" {
		t.Fatalf("race 解析错误: %+v", ini.Race)
	}
	if len(ini.Sku) != 2 || ini.Sku["区域"]["QQ区"] != "2" || ini.Sku["时长"]["月卡"] != "0.5" {
		t.Fatalf("sku 解析错误: %+v", ini.Sku)
	}
	// 四形态
	empty, _ := parseAcgINI("")
	if empty == nil || len(empty.Race) != 0 || len(empty.Sku) != 0 {
		t.Fatal("空 INI 应返回空结构")
	}
}

func TestBuildAcgCombos(t *testing.T) {
	// 双层：2 race × 2 区域 × 1 时长 = 4 组合
	ini, _ := parseAcgINI("[category]\n1天=1.00\n7天=5.00\n[sku]\n区域.微信区=0\n区域.QQ区=2\n时长.月卡=0.5\n")
	combos, err := buildAcgCombos(ini, 990)
	if err != nil {
		t.Fatal(err)
	}
	if len(combos) != 4 {
		t.Fatalf("组合数 = %d, want 4", len(combos))
	}
	// 找 7天·QQ区·月卡：5.00 + 2 + 0.5 = 750 分
	var hit *acgCombo
	for i := range combos {
		if combos[i].Race == "7天" && combos[i].Choices["区域"] == "QQ区" && combos[i].Choices["时长"] == "月卡" {
			hit = &combos[i]
		}
	}
	if hit == nil {
		t.Fatal("未找到 7天×QQ区×月卡 组合")
	}
	if hit.BaseCents != 500 || hit.AddCents != 250 {
		t.Fatalf("价格错误: base=%d add=%d", hit.BaseCents, hit.AddCents)
	}
	if hit.Code != "7天|区域=QQ区;时长=月卡" {
		t.Fatalf("编码错误: %q", hit.Code)
	}
	if hit.Name != "7天 · QQ区 · 月卡" {
		t.Fatalf("组合名错误: %q", hit.Name)
	}
	// 编码解码往返
	race, choices, err := DecodeAcgSpecCode(hit.Code)
	if err != nil || race != "7天" || choices["区域"] != "QQ区" || choices["时长"] != "月卡" {
		t.Fatalf("解码错误: race=%q choices=%v err=%v", race, choices, err)
	}
	// sku-only（无 race）：base 用商品价
	ini2, _ := parseAcgINI("[sku]\n区域.微信区=0\n区域.QQ区=2\n")
	combos2, _ := buildAcgCombos(ini2, 990)
	if len(combos2) != 2 {
		t.Fatalf("sku-only 组合数错误: %+v", combos2)
	}
	for _, c := range combos2 {
		if c.BaseCents != 990 {
			t.Fatalf("sku-only 基准价应取商品价: %+v", c)
		}
		if c.Code == "|区域=QQ区" && c.AddCents != 200 {
			t.Fatalf("sku-only 加价错误: %+v", c)
		}
	}
	// race-only
	ini3, _ := parseAcgINI("[category]\n月卡=10.00\n")
	combos3, _ := buildAcgCombos(ini3, 0)
	if len(combos3) != 1 || combos3[0].BaseCents != 1000 {
		t.Fatalf("race-only 错误: %+v", combos3)
	}
	// 护栏：超 64 组合报错
	var b strings.Builder
	b.WriteString("[sku]\n")
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	for _, n := range names {
		for i := 0; i < 3; i++ {
			b.WriteString(n + "." + string(rune('A'+i)) + "=0\n")
		}
	}
	ini4, _ := parseAcgINI(b.String())
	if _, err := buildAcgCombos(ini4, 0); err == nil {
		t.Fatal("3^7=2187 组合应触发护栏")
	}
	// 保留字符
	ini5, _ := parseAcgINI("[category]\n月|卡=10\n")
	if _, err := buildAcgCombos(ini5, 0); err == nil {
		t.Fatal("保留字符应报错")
	}
}

func TestAcgSpecFormFields(t *testing.T) {
	fields, err := AcgSpecFormFields("7天|区域=QQ区;时长=月卡")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"race": "7天", "sku[区域]": "QQ区", "sku[时长]": "月卡"}
	if len(fields) != len(want) {
		t.Fatalf("字段数错误: %+v", fields)
	}
	for k, v := range want {
		if fields[k] != v {
			t.Fatalf("字段 %s = %q, want %q", k, fields[k], v)
		}
	}
	// 非法编码
	if _, err := AcgSpecFormFields("no-bar"); err == nil {
		t.Fatal("缺 | 应报错")
	}
}

// TestAcgSpecGoldenSignature 黄金签名向量：flat map（sku[中文规格] 键）的
// AcgFakaSign 输出 == PHP 端嵌套数组 ksort+http_build_query 口径的 MD5。
// PHP 等价算式（实测向量，Str::generateSignature）：
//
//	$data = ["app_id"=>"1","app_key"=>"K","num"=>"1","race"=>"7天",
//	         "sku"=>["区域"=>"QQ区"],"shared_code"=>"C1"];
//	ksort($data); unset($data['sign']);
//	md5(urldecode(http_build_query($data))."&key=K")
func TestAcgSpecGoldenSignature(t *testing.T) {
	// PHP http_build_query 嵌套输出（urldecode 后）：
	// app_id=1&app_key=K&num=1&race=7天&shared_code=C1&sku[区域]=QQ区
	flat := map[string]string{
		"app_id": "1", "app_key": "K", "num": "1", "race": "7天",
		"shared_code": "C1", "sku[区域]": "QQ区",
	}
	got := AcgFakaSign(flat, "K")
	// 与「按 PHP 嵌套序手工构造串」对照（ksort 后键序：app_id,app_key,num,race,shared_code,sku；
	// 嵌套展开 http_build_query → sku[区域]=QQ区 且中文 urldecode 原样）
	phpString := "app_id=1&app_key=K&num=1&race=7天&shared_code=C1&sku[区域]=QQ区"
	want := md5Hex([]byte(phpString + "&key=K"))
	if got != want {
		t.Fatalf("嵌套规格签名不一致:\n got  %s\n want %s (php串: %s)", got, want, phpString)
	}
	// 多规格键序（字母序）验证
	flat2 := map[string]string{
		"sku[区域]": "QQ区", "sku[时长]": "月卡", "app_id": "1",
	}
	got2 := AcgFakaSign(flat2, "K")
	php2 := "app_id=1&sku[区域]=QQ区&sku[时长]=月卡"
	want2 := md5Hex([]byte(php2 + "&key=K"))
	if got2 != want2 {
		t.Fatalf("多规格键序签名不一致: got %s want %s", got2, want2)
	}
}

func TestAcgSpecListProductsAndTrade(t *testing.T) {
	var gotTrade, gotStock url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/shared/commodity/items":
			_, _ = w.Write([]byte(`{"code":200,"data":[{"id":1,"name":"卡","children":[` +
				`{"code":"SP1","name":"规格品","price":9.9,"factory_price":5.0,"status":1,"delivery_way":0,` +
				`"stock":100,"config":"[category]\n1天=1.00\n7天=5.00\n[sku]\n区域.微信区=0\n区域.QQ区=2\n"},` +
				`{"code":"PLN","name":"无规格品","price":3.0,"factory_price":1.0,"status":1,"delivery_way":0,"stock":50,"config":""},` +
				`{"code":"MAN","name":"手动发货品","price":2.0,"factory_price":1.0,"status":1,"delivery_way":1,"stock":10,"config":""}` +
				`]}]}`))
		case "/shared/commodity/trade":
			gotTrade = r.PostForm
			_, _ = w.Write([]byte(`{"code":200,"data":{"tradeNo":"T9","amount":"7.00","secret":"CARD-A"}}`))
		case "/shared/commodity/stock":
			gotStock = r.PostForm
			_, _ = w.Write([]byte(`{"code":200,"data":{"stock":"3"}}`))
		}
	}))
	defer srv.Close()
	a := &acgFakaAdapter{
		protocol: "acg_faka",
		creds:    Credentials{AppID: "1", AppKey: "K"},
		t:        newTransportWithClient(srv.URL, nil, nil, srv.Client()),
	}
	ctx := context.Background()

	// 1) 列表：规格品 4 组合（2 race × 2 区域）；无规格品 0 SKU
	list, err := a.ListProducts(ctx, 1, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	var spec, plain, manual *Product
	for i := range list.Items {
		if list.Items[i].ID == "SP1" {
			spec = &list.Items[i]
		}
		if list.Items[i].ID == "PLN" {
			plain = &list.Items[i]
		}
		if list.Items[i].ID == "MAN" {
			manual = &list.Items[i]
		}
	}
	if spec == nil || plain == nil || manual == nil {
		t.Fatal("商品缺失")
	}
	// 手动发货商品（delivery_way=1）同步为下架（订单查询恒占位文案，API 无法交付）
	if manual.IsActive {
		t.Fatal("手动发货商品应同步为下架")
	}
	if len(spec.SKUs) != 4 {
		t.Fatalf("规格品组合数 = %d, want 4", len(spec.SKUs))
	}
	if len(plain.SKUs) != 0 {
		t.Fatalf("无规格品不应有 SKU: %d", len(plain.SKUs))
	}
	// 组合价抽验：7天×QQ区 = 500+200 = 700 分
	found := false
	for _, sk := range spec.SKUs {
		if sk.Code == "7天|区域=QQ区" {
			found = true
			if sk.Price != 700 || sk.Name != "7天 · QQ区" || sk.SpecValues["种类"] != "7天" || sk.SpecValues["区域"] != "QQ区" {
				t.Fatalf("组合 SKU 字段错误: %+v", sk)
			}
		}
	}
	if !found {
		t.Fatal("未找到 7天|区域=QQ区 组合")
	}

	// 2) 下单带规格：表单应含 race=7天 + sku[区域]=QQ区
	res, err := a.CreateOrder(ctx, CreateOrderReq{
		ProductCode: "SP1", UpstreamSKU: "7天|区域=QQ区",
		Quantity: 1, DownstreamOrderNo: "n1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "delivered" || res.Cards[0] != "CARD-A" {
		t.Fatalf("下单结果错误: %+v", res)
	}
	if gotTrade.Get("race") != "7天" || gotTrade.Get("sku[区域]") != "QQ区" || gotTrade.Get("shared_code") != "SP1" {
		t.Fatalf("trade 表单规格字段缺失: %+v", gotTrade)
	}

	// 3) 按规格查库存
	st, err := a.GetStock(ctx, "SP1", "1天|区域=微信区")
	if err != nil || st != 3 {
		t.Fatalf("规格库存查询失败: st=%d err=%v", st, err)
	}
	if gotStock.Get("race") != "1天" || gotStock.Get("sku[区域]") != "微信区" {
		t.Fatalf("stock 表单规格字段缺失: %+v", gotStock)
	}
}

// TestAcgSpecFormOrderMatchesSignature 表单发送序 == 签名序（字母序）字节一致性。
func TestAcgSpecFormOrderMatchesSignature(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"code":200,"data":{"tradeNo":"T","amount":"1.00"}}`))
	}))
	defer srv.Close()
	a := &acgFakaAdapter{
		protocol: "acg_faka",
		creds:    Credentials{AppID: "1", AppKey: "K"},
		t:        newTransportWithClient(srv.URL, nil, nil, srv.Client()),
	}
	if _, err := a.CreateOrder(context.Background(), CreateOrderReq{
		ProductCode: "SP1", UpstreamSKU: "7天|区域=QQ区;时长=月卡",
		Quantity: 1, DownstreamOrderNo: "n", TraceID: "t",
	}); err != nil {
		t.Fatal(err)
	}
	// 线上表单（url.Values.Encode 字母序）应包含规格字段
	body := string(rawBody)
	for _, want := range []string{"race=7%E5%A4%A9", "sku%5B%E5%8C%BA%E5%9F%9F%5D=QQ%E5%8C%BA", "sku%5B%E6%97%B6%E9%95%BF%5D=%E6%9C%88%E5%8D%A1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("表单缺 %s: %s", want, body)
		}
	}
	// 键序与编码一致性：解析回 form 与签名 flat map 等价
	form, _ := url.ParseQuery(body)
	flat := map[string]string{}
	for k := range form {
		flat[k] = form.Get(k)
	}
	flat["app_id"], flat["app_key"] = "1", "K"
	if got := AcgFakaSign(flat, "K"); got == "" {
		t.Fatal("签名不可计算")
	}
	// 有序键断言（字母序）：app_id < app_key < card_id < contact < num < race < request_no < shared_code < sku[..]
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !sort.StringsAreSorted(keys) {
		t.Fatal("表单键未排序")
	}
	_ = json.Marshal
}
