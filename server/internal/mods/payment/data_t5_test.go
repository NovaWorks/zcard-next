package payment

// 渠道配置面后端契约测试：
// ListDrivers 驱动元数据完整性 / CreateChannel 凭据即时校验 /
// channelPB 脱敏回显（敏感掩码 + configured_fields + callback_url）/
// UpdateChannel fee_type + 凭据变更校验 / storefront ListChannels 启用过滤。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// TestListDrivers 驱动元数据：wallet 内置 + 六 adapter + 字段 schema 完整。
func TestListDrivers(t *testing.T) {
	_, repo, _, _, _, _ := newCallbackEnv(t)
	svc := NewAdminPaymentService(repo, nil)
	// 需要 data 才能 channelPB？ListDrivers 不碰 data——直接用 repo
	list, err := svc.ListDrivers(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	byCode := map[string]*adminv1.Driver{}
	for _, d := range list.Drivers {
		byCode[d.Code] = d
	}
	// 内置驱动齐备
	for _, code := range []string{"wallet", "alipay", "wechat", "epay", "epusdt", "stripe", "paypal"} {
		if byCode[code] == nil {
			t.Fatalf("缺驱动 %s", code)
		}
	}
	// 元数据字段
	if byCode["paypal"].Name == "" || byCode["paypal"].Icon == "" || byCode["paypal"].Description == "" {
		t.Fatalf("paypal 元数据不完整: %+v", byCode["paypal"])
	}
	// 字段 schema：paypal 含 client_id/client_secret/mode；敏感标记正确
	pf := map[string]*adminv1.ConfigField{}
	for _, f := range byCode["paypal"].Fields {
		pf[f.Key] = f
	}
	if pf["client_id"] == nil || pf["client_secret"] == nil || pf["mode"] == nil {
		t.Fatalf("paypal 字段 schema 不完整: %+v", byCode["paypal"].Fields)
	}
	if !pf["client_secret"].Sensitive || pf["client_secret"].Type != "password" {
		t.Fatalf("client_secret 应为敏感 password 字段")
	}
	if pf["mode"].Type != "select" || len(pf["mode"].Options) != 2 {
		t.Fatalf("mode 应为双选项 select")
	}
	// 自定义驱动（无 schema 声明）兜底：注册表内全部为内置驱动，验证 schema 非空
	for _, d := range list.Drivers {
		if d.Code != "wallet" && len(d.Fields) == 0 {
			t.Fatalf("驱动 %s 缺字段 schema", d.Code)
		}
	}
}

// TestCreateChannelValidateConfig 创建时凭据即时校验（驱动未实现/配置无效拒绝）。
func TestCreateChannelValidateConfig(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	svc := NewAdminPaymentService(repo, d)
	ctx := context.Background()

	// 驱动未实现
	_, err := svc.CreateChannel(ctx, &adminv1.CreateChannelRequest{
		Name: "x", Code: "x1", Driver: "notexist", ConfigJson: `{}`,
	})
	if !isBadRequest(err, "payment.DRIVER_UNSUPPORTED") {
		t.Fatalf("驱动未实现应 400 DRIVER_UNSUPPORTED: %v", err)
	}
	// 空凭据创建成功（待配置状态——勾选式添加路径）
	empty, err := svc.CreateChannel(ctx, &adminv1.CreateChannelRequest{
		Name: "Alipay", Code: "alipay2", Driver: "alipay", ConfigJson: `{}`, Enabled: true,
	})
	if err != nil {
		t.Fatalf("空凭据创建应成功（待配置）: %v", err)
	}
	if len(empty.ConfiguredFields) != 0 {
		t.Fatalf("空凭据渠道 configured_fields 应为空: %+v", empty.ConfiguredFields)
	}
	// 配置无效（paypal 缺 client_secret——提交实际配置时才校验）
	_, err = svc.CreateChannel(ctx, &adminv1.CreateChannelRequest{
		Name: "paypal", Code: "paypal2", Driver: "paypal", ConfigJson: `{"client_id":"x"}`,
	})
	if !isBadRequest(err, "payment.CHANNEL_CONFIG_INVALID") {
		t.Fatalf("凭据无效应 400 CHANNEL_CONFIG_INVALID: %v", err)
	}
	// 合法凭据创建成功（敏感字段加密落库）
	ch, err := svc.CreateChannel(ctx, &adminv1.CreateChannelRequest{
		Name: "PayPal", Code: "paypal2", Driver: "paypal",
		ConfigJson: `{"client_id":"cid","client_secret":"cs","mode":"sandbox"}`,
		Enabled:    true, Fee: 0, FeeType: "fixed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Code != "paypal2" || len(ch.ConfiguredFields) != 3 {
		t.Fatalf("创建结果错位: %+v", ch)
	}
}

// TestChannelPBMaskedEcho 脱敏回显：敏感字段 ****、非敏感显值、configured_fields、
// callback_url（settings 未装配 → 相对路径）。
func TestChannelPBMaskedEcho(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	svc := NewAdminPaymentService(repo, d)
	ctx := context.Background()

	ch, err := svc.CreateChannel(ctx, &adminv1.CreateChannelRequest{
		Name: "Stripe", Code: "stripe2", Driver: "stripe",
		ConfigJson: `{"secret_key":"sk_live_x","webhook_secret":"whsec_y","target_currency":"USD"}`,
		Enabled:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 回显：敏感字段掩码、非敏感显值
	var cfg map[string]string
	if err := json.Unmarshal([]byte(ch.ConfigJson), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["secret_key"] != "****" || cfg["webhook_secret"] != "****" {
		t.Fatalf("敏感字段应掩码: %+v", cfg)
	}
	if cfg["target_currency"] != "USD" {
		t.Fatalf("非敏感字段应显值: %+v", cfg)
	}
	if len(ch.ConfiguredFields) != 3 {
		t.Fatalf("configured_fields 应为 3: %+v", ch.ConfiguredFields)
	}
	if ch.CallbackUrl != "/payments/callback/stripe2" {
		t.Fatalf("callback_url 错位（settings 未装配应相对路径）: %s", ch.CallbackUrl)
	}
	// 列表同样脱敏
	list, err := svc.ListChannels(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list.Channels {
		if c.Code == "stripe2" && c.ConfigJson != ch.ConfigJson {
			t.Fatalf("列表回显与详情不一致: %s vs %s", c.ConfigJson, ch.ConfigJson)
		}
	}
}

// TestUpdateChannelFeeTypeAndValidate 更新：fee_type 传递 + 凭据变更校验 + **** 跳过。
func TestUpdateChannelFeeTypeAndValidate(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	svc := NewAdminPaymentService(repo, d)
	ctx := context.Background()

	ch, err := svc.CreateChannel(ctx, &adminv1.CreateChannelRequest{
		Name: "PayPal", Code: "paypal3", Driver: "paypal",
		ConfigJson: `{"client_id":"cid","client_secret":"cs","mode":"live"}`,
		Enabled:    true, Fee: 100, FeeType: "fixed",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 凭据变更校验：无效配置拒绝
	_, err = svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{
		Id: ch.Id, ConfigJson: `{"client_id":"cid"}`,
	})
	if !isBadRequest(err, "payment.CHANNEL_CONFIG_INVALID") {
		t.Fatalf("无效凭据应拒绝: %v", err)
	}
	// 敏感字段留空不覆盖（config_json=****）+ fee_type 更新
	upd, err := svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{
		Id: ch.Id, ConfigJson: `"****"`, Fee: 150, FeeType: "percent", Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Fee != 150 || upd.FeeType != "percent" || upd.Enabled {
		t.Fatalf("fee/fee_type/enabled 更新错位: %+v", upd)
	}
	var cfg map[string]string
	_ = json.Unmarshal([]byte(upd.ConfigJson), &cfg)
	// 敏感字段保持原值（掩码回显）；非敏感字段显值
	if cfg["client_secret"] != "****" || cfg["client_id"] != "cid" || cfg["mode"] != "live" {
		t.Fatalf("**** 应保持原凭据（掩码回显）: %+v", cfg)
	}
	// fee_type 非法拒绝
	_, err = svc.UpdateChannel(ctx, &adminv1.UpdateChannelRequest{Id: ch.Id, FeeType: "half"})
	if !isBadRequest(err, "payment.INVALID_INPUT") {
		t.Fatalf("非法 fee_type 应拒绝: %v", err)
	}
}

// TestStorefrontListChannels 启用渠道过滤（停用渠道不下发）。
func TestStorefrontListChannels(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	svc := NewStorePaymentService(repo, d)
	ctx := context.Background()

	// 建一个停用渠道
	if _, err := svc.repo.CreateChannel(ctx, "停用", "disabled1", "epay", `{"pid":"1","key":"k"}`, 0, "fixed", false, 0, "", nil); err != nil {
		t.Fatal(err)
	}
	// 建一个启用但未配置的渠道（待配置——不应下发）
	if _, err := svc.repo.CreateChannel(ctx, "待配置", "unconf1", "stripe", `{}`, 0, "fixed", true, 0, "", nil); err != nil {
		t.Fatal(err)
	}
	reply, err := svc.ListChannels(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, c := range reply.Channels {
		codes[c.Code] = true
		if c.Name == "" {
			t.Fatalf("渠道缺显示名: %+v", c)
		}
	}
	// 游客：真实渠道可下发，wallet（余额）不下发
	if !codes["epay"] {
		t.Fatalf("启用渠道缺失: %+v", codes)
	}
	if codes["balance"] {
		t.Fatal("游客不应看到余额渠道")
	}
	if codes["disabled1"] {
		t.Fatal("停用渠道不应下发")
	}
	if codes["unconf1"] {
		t.Fatal("待配置渠道（空凭据）不应下发")
	}
	// 登录态：余额渠道可见
	authCtx := identity.WithClaims(ctx, &authn.Claims{Subject: 1, Realm: authn.RealmUser})
	reply2, err := svc.ListChannels(authCtx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	hasBalance := false
	for _, c := range reply2.Channels {
		if c.Code == "balance" {
			hasBalance = true
		}
	}
	if !hasBalance {
		t.Fatalf("登录态应可见余额渠道: %+v", reply2.Channels)
	}
}

// TestFieldOptionsEndpoint 动态选项端点：转发驱动 OptionProvider + 不支持拒绝。
func TestFieldOptionsEndpoint(t *testing.T) {
	_, repo, _, _, _, _ := newCallbackEnv(t)
	svc := NewAdminPaymentService(repo, nil)
	ctx := context.Background()

	// epusdt 支持动态选项；api_url 缺失 → 静态矩阵（非 fallback）
	reply, err := svc.FieldOptions(ctx, &adminv1.FieldOptionsRequest{Code: "epusdt", Field: "network", ConfigJson: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Fallback || len(reply.Options) < 6 {
		t.Fatalf("静态回落错位: %+v", reply)
	}
	// token 静态矩阵
	reply, err = svc.FieldOptions(ctx, &adminv1.FieldOptionsRequest{Code: "epusdt", Field: "token", ConfigJson: `{}`})
	if err != nil || len(reply.Options) != 5 {
		t.Fatalf("token 静态矩阵错位: %v %+v", err, reply)
	}
	// 未知驱动 → 400
	_, err = svc.FieldOptions(ctx, &adminv1.FieldOptionsRequest{Code: "notexist", Field: "network"})
	if !isBadRequest(err, "payment.DRIVER_UNSUPPORTED") {
		t.Fatalf("未知驱动应 400: %v", err)
	}
	// 不支持动态选项的驱动（alipay）→ 400
	_, err = svc.FieldOptions(ctx, &adminv1.FieldOptionsRequest{Code: "alipay", Field: "app_id"})
	if !isBadRequest(err, "payment.OPTIONS_UNSUPPORTED") {
		t.Fatalf("不支持动态选项应 400: %v", err)
	}
	// 缺参数 → 400
	_, err = svc.FieldOptions(ctx, &adminv1.FieldOptionsRequest{})
	if !isBadRequest(err, "payment.INVALID_INPUT") {
		t.Fatalf("缺参应 400: %v", err)
	}
}

// isBadRequest kratos 错误断言。
func isBadRequest(err error, reason string) bool {
	if err == nil {
		return false
	}
	se := errors.FromError(err)
	return se.Reason == reason && se.Code == 400
}

var _ = strings.TrimSpace
var _ = storefrontv1.ChannelListReply{}
