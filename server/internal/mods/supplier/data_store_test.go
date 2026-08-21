package supplier

// 前台对接申请链路：申请 → 审核（通过/驳回）→ 凭据查看/重置 → 撤销；
// 属主越权拒绝、防刷护栏、凭据格式兼容三面板。

import (
	"context"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplieraccount"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"google.golang.org/protobuf/types/known/emptypb"
)

func newStoreSupplierService(t *testing.T) (*StoreSupplierService, *SupplierRepoImpl) {
	t.Helper()
	repo, _ := newSupplierTestData(t)
	return NewStoreSupplierService(repo, nil, nil), repo
}

// fakeRechargePayer 记录调用并返回固定支付信息（充值测试替身）。
type fakeRechargePayer struct {
	calls int
}

func (f *fakeRechargePayer) CreateRechargePayment(ctx context.Context, rechargeOrderID uint64, channel string, amount money.Cents) (*port.RechargePaymentInfo, error) {
	f.calls++
	return &port.RechargePaymentInfo{PaymentID: 900 + uint64(f.calls), Type: "redirect", Payload: "https://pay.example.com/"}, nil
}

func userCtx(uid uint64) context.Context {
	return identity.WithClaims(context.Background(), &authn.Claims{Subject: uid, Realm: authn.RealmUser})
}

func TestStoreSubmitApplication(t *testing.T) {
	svc, repo := newStoreSupplierService(t)
	acc, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "acg_faka", DisplayName: "我的小店", Contact: "qq:123",
		ApplyReason: "想接入供货", NotifyUrl: "https://shop.example.com/cb",
	})
	if err != nil {
		t.Fatal(err)
	}
	if acc.Status != "applying" || acc.Protocol != "acg_faka" || acc.DisplayName != "我的小店" {
		t.Fatalf("账户字段异常: %+v", acc)
	}
	if len(acc.ApiKey) != 32 || hex.DecodedLen(len(acc.ApiKey)) != 16 {
		t.Fatalf("api_key 必须为 16 字节 hex（32 字符，acg app_id ≤32 校验）: %q", acc.ApiKey)
	}
	if _, ok := any(acc).(interface{ GetApiSecret() string }); ok {
		t.Fatalf("申请响应不得泄漏 secret")
	}
	if acc.ReviewNote != "" || acc.ApplyReason != "想接入供货" {
		t.Fatalf("申请字段异常: %+v", acc)
	}

	// 协议白名单
	if _, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "mcy", DisplayName: "x",
	}); err == nil {
		t.Fatal("非法协议应拒绝")
	}
	// 站点名必填
	if _, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: " ",
	}); err == nil {
		t.Fatal("空站点名应拒绝")
	}
	// 回调地址：http 与 https 均支持；裸域名自动补 https://
	httpAcc, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "http站", NotifyUrl: "http://plain.example.com/cb",
	})
	if err != nil {
		t.Fatalf("http 回调应被接受: %v", err)
	}
	if httpAcc.Id == 0 {
		t.Fatal("http 回调建户失败")
	}
	domainAcc, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "裸域名站", NotifyUrl: "shop.example.com/cb",
	})
	if err != nil {
		t.Fatalf("裸域名回调应被接受（自动补 https）: %v", err)
	}
	// 裸域名归一校验：落库后 notify_url 带 https:// 前缀
	stored, err := repo.GetAccount(context.Background(), domainAcc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.NotifyURL != "https://shop.example.com/cb" {
		t.Fatalf("裸域名应归一为 https:// 前缀: %q", stored.NotifyURL)
	}
	// 非法回调格式拒绝
	if _, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "x", NotifyUrl: "ftp://bad.example.com/cb",
	}); err == nil {
		t.Fatal("非 http/https 回调应拒绝")
	}
	// 未登录拒绝
	if _, err := svc.SubmitSupplierApplication(context.Background(), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "x",
	}); err == nil {
		t.Fatal("未登录应拒绝")
	}
}

func TestStoreApplicationLimit(t *testing.T) {
	svc, _ := newStoreSupplierService(t)
	// applying 上限 5：第 6 个拒绝
	for i := 0; i < maxApplyingPerUser; i++ {
		if _, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
			Protocol: "zcard", DisplayName: "店",
		}); err != nil {
			t.Fatalf("第 %d 个申请应成功: %v", i+1, err)
		}
	}
	if _, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "店",
	}); err == nil {
		t.Fatal("第 6 个申请中应拒绝")
	}
}

func TestStoreCredentialsLifecycle(t *testing.T) {
	svc, repo := newStoreSupplierService(t)

	// 申请（用户 1）
	acc, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "dujiao_next", DisplayName: "站A",
	})
	if err != nil {
		t.Fatal(err)
	}
	// applying 时查看凭据被拒
	if _, err := svc.GetSupplierCredentials(userCtx(1), &storefrontv1.GetSupplierCredentialsRequest{Id: acc.Id}); err == nil {
		t.Fatal("未审核通过不得查看凭据")
	}

	// admin 审核通过
	reviewed, err := repo.ReviewAccount(context.Background(), acc.Id, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.Status != supplieraccount.StatusApproved {
		t.Fatalf("审核通过后状态应为 approved: %s", reviewed.Status)
	}

	// 查看凭据：格式 32/64 hex
	cred, err := svc.GetSupplierCredentials(userCtx(1), &storefrontv1.GetSupplierCredentialsRequest{Id: acc.Id})
	if err != nil {
		t.Fatal(err)
	}
	if cred.ApiKey != acc.ApiKey || len(cred.ApiSecret) != 64 || hex.DecodedLen(len(cred.ApiSecret)) != 32 {
		t.Fatalf("凭据异常: key=%q secret=%q", cred.ApiKey, cred.ApiSecret)
	}

	// 重置：新 secret 与旧不同且旧立即失效（按新 secret 验签通过）
	oldSecret := cred.ApiSecret
	newCred, err := svc.RegenerateSupplierSecret(userCtx(1), &storefrontv1.RegenerateSupplierSecretRequest{Id: acc.Id})
	if err != nil {
		t.Fatal(err)
	}
	if newCred.ApiSecret == oldSecret || len(newCred.ApiSecret) != 64 {
		t.Fatalf("重置后 secret 应更换")
	}
	// 重置后账户按新 secret 可验签（CredentialsOf 解密成功即证明入库的是新值）
	_, plain, err := repo.CredentialsOf(context.Background(), acc.Id)
	if err != nil || plain != newCred.ApiSecret {
		t.Fatalf("重置后入库 secret 与返回不一致: %v", err)
	}
}

func TestStoreOwnershipAndReject(t *testing.T) {
	svc, repo := newStoreSupplierService(t)

	acc, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "用户1的站",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 用户 2 越权访问 → 404（不暴露存在性）
	if _, err := svc.GetSupplierCredentials(userCtx(2), &storefrontv1.GetSupplierCredentialsRequest{Id: acc.Id}); err == nil {
		t.Fatal("越权查看应拒绝")
	}
	if _, err := svc.RegenerateSupplierSecret(userCtx(2), &storefrontv1.RegenerateSupplierSecretRequest{Id: acc.Id}); err == nil {
		t.Fatal("越权重置应拒绝")
	}
	if _, err := svc.CancelSupplierApplication(userCtx(2), &storefrontv1.CancelSupplierApplicationRequest{Id: acc.Id}); err == nil {
		t.Fatal("越权撤销应拒绝")
	}

	// admin 驳回（带意见）→ rejected；用户侧凭据仍不可用
	reviewed, err := repo.ReviewAccount(context.Background(), acc.Id, false, "资料不完整，请补充联系方式")
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.Status != supplieraccount.StatusRejected || reviewed.ReviewNote != "资料不完整，请补充联系方式" {
		t.Fatalf("驳回状态/意见异常: %+v", reviewed)
	}
	if _, err := svc.GetSupplierCredentials(userCtx(1), &storefrontv1.GetSupplierCredentialsRequest{Id: acc.Id}); err == nil {
		t.Fatal("驳回后不得查看凭据")
	}
	// 驳回后不可撤销（仅 applying）
	if _, err := svc.CancelSupplierApplication(userCtx(1), &storefrontv1.CancelSupplierApplicationRequest{Id: acc.Id}); err == nil {
		t.Fatal("非 applying 状态不可撤销")
	}

	// 用户侧列表可见驳回状态与意见
	list, err := svc.ListMySupplierAccounts(userCtx(1), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Accounts) != 1 || list.Accounts[0].Status != "rejected" || !strings.Contains(list.Accounts[0].ReviewNote, "资料不完整") {
		t.Fatalf("列表状态/意见异常: %+v", list.Accounts)
	}
}

func TestStoreCancelApplication(t *testing.T) {
	svc, _ := newStoreSupplierService(t)

	acc, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "acg_faka", DisplayName: "取消测试",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CancelSupplierApplication(userCtx(1), &storefrontv1.CancelSupplierApplicationRequest{Id: acc.Id}); err != nil {
		t.Fatal(err)
	}
	// 撤销后列表为空
	list, err := svc.ListMySupplierAccounts(userCtx(1), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Accounts) != 0 {
		t.Fatalf("撤销后应无记录: %+v", list.Accounts)
	}
}

func TestStoreSupplierRecharge(t *testing.T) {
	repo, _ := newSupplierTestData(t)
	payer := &fakeRechargePayer{}
	svc := NewStoreSupplierService(repo, nil, payer)

	// 申请 + 审核通过
	acc, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "充值测试站",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReviewAccount(context.Background(), acc.Id, true, ""); err != nil {
		t.Fatal(err)
	}

	// 正常充值：档位内金额（缺省 min=1000 分）→ 建单 + payer 发起
	reply, err := svc.CreateSupplierRecharge(userCtx(1), &storefrontv1.CreateSupplierRechargeRequest{
		Id: acc.Id, AmountCents: 10000, Channel: "alipay",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply.PaymentId == 0 || reply.Type != "redirect" || payer.calls != 1 {
		t.Fatalf("充值发起异常: %+v calls=%d", reply, payer.calls)
	}
	// 充值单落库：target=supply + supplier_account_id + pending
	ro, err := repo.data.Client.RechargeOrder.Get(context.Background(), reply.RechargeId)
	if err != nil {
		t.Fatal(err)
	}
	if ro.Target != "supply" || ro.SupplierAccountID != acc.Id || ro.Amount != 10000 || ro.Status != "pending" {
		t.Fatalf("充值单字段异常: %+v", ro)
	}

	// 未审核通过（新申请）拒绝充值
	acc2, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "未审核站",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSupplierRecharge(userCtx(1), &storefrontv1.CreateSupplierRechargeRequest{
		Id: acc2.Id, AmountCents: 10000, Channel: "alipay",
	}); err == nil {
		t.Fatal("未审核账户应拒绝充值")
	}

	// 越权：用户 2 给用户 1 的账户充值 → 404
	if _, err := svc.CreateSupplierRecharge(userCtx(2), &storefrontv1.CreateSupplierRechargeRequest{
		Id: acc.Id, AmountCents: 10000, Channel: "alipay",
	}); err == nil {
		t.Fatal("越权充值应拒绝")
	}

	// 金额档位外（< min 1000 分）拒绝
	if _, err := svc.CreateSupplierRecharge(userCtx(1), &storefrontv1.CreateSupplierRechargeRequest{
		Id: acc.Id, AmountCents: 100, Channel: "alipay",
	}); err == nil {
		t.Fatal("低于最低限额应拒绝")
	}

	// 余额视图：账户列表带 balance_cache（充值未入账仍为 0——入账在支付回调）
	list, err := svc.ListMySupplierAccounts(userCtx(1), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list.Accounts {
		if a.Id == acc.Id && a.BalanceCache != 0 {
			t.Fatalf("支付前不得入账: %+v", a)
		}
	}
}

func TestIPWhitelistMatch(t *testing.T) {
	r := func(xff, remote string) *http.Request {
		req, _ := http.NewRequest("POST", "/api/supply/orders", nil)
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		req.RemoteAddr = remote
		return req
	}

	// 空名单 = 全放行
	if !ipAllowed(nil, r("", "1.2.3.4:5678")) {
		t.Fatal("空名单应放行")
	}
	if !ipAllowed([]string{}, r("", "1.2.3.4:5678")) {
		t.Fatal("空名单（空切片）应放行")
	}
	// 精确 IP：命中 / 未命中；XFF 优先于 RemoteAddr
	wl := []string{"1.2.3.4"}
	if !ipAllowed(wl, r("", "1.2.3.4:9999")) {
		t.Fatal("精确 IP 应命中")
	}
	if ipAllowed(wl, r("", "1.2.3.5:9999")) {
		t.Fatal("未命中 IP 应拒绝")
	}
	if !ipAllowed(wl, r("1.2.3.4, 10.0.0.1", "10.0.0.1:9999")) {
		t.Fatal("XFF 首段应优先")
	}
	if ipAllowed(wl, r("9.9.9.9", "1.2.3.4:9999")) {
		t.Fatal("XFF 存在时应以 XFF 为准（未命中拒绝）")
	}
	// CIDR
	cidr := []string{"10.0.0.0/24"}
	if !ipAllowed(cidr, r("", "10.0.0.55:1234")) {
		t.Fatal("CIDR 内应命中")
	}
	if ipAllowed(cidr, r("", "10.0.1.1:1234")) {
		t.Fatal("CIDR 外应拒绝")
	}
	// 条目校验
	if err := ValidateIPWhitelistEntry("1.2.3.4"); err != nil {
		t.Fatalf("合法 IP 被拒: %v", err)
	}
	if err := ValidateIPWhitelistEntry("10.0.0.0/8"); err != nil {
		t.Fatalf("合法 CIDR 被拒: %v", err)
	}
	if err := ValidateIPWhitelistEntry("not-an-ip"); err == nil {
		t.Fatal("非法条目应拒绝")
	}
	if err := ValidateIPWhitelistEntry("10.0.0.0/99"); err == nil {
		t.Fatal("非法掩码应拒绝")
	}
}

func TestStoreIPWhitelist(t *testing.T) {
	repo, _ := newSupplierTestData(t)
	svc := NewStoreSupplierService(repo, nil, nil)

	// 申请 + 审核通过
	acc, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "白名单测试",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReviewAccount(context.Background(), acc.Id, true, ""); err != nil {
		t.Fatal(err)
	}

	// 设置白名单（混合精确 IP + CIDR；含重复与空白——应清洗）
	updated, err := svc.SetSupplierIPWhitelist(userCtx(1), &storefrontv1.SetSupplierIPWhitelistRequest{
		Id: acc.Id, Ips: []string{" 1.2.3.4 ", "10.0.0.0/24", "1.2.3.4", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.IpWhitelist) != 2 || updated.IpWhitelist[0] != "1.2.3.4" || updated.IpWhitelist[1] != "10.0.0.0/24" {
		t.Fatalf("白名单清洗异常: %v", updated.IpWhitelist)
	}

	// 非法条目拒绝
	if _, err := svc.SetSupplierIPWhitelist(userCtx(1), &storefrontv1.SetSupplierIPWhitelistRequest{
		Id: acc.Id, Ips: []string{"bad-entry"},
	}); err == nil {
		t.Fatal("非法白名单条目应拒绝")
	}

	// 清空 = 所有 IP 放行
	updated, err = svc.SetSupplierIPWhitelist(userCtx(1), &storefrontv1.SetSupplierIPWhitelistRequest{
		Id: acc.Id, Ips: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.IpWhitelist) != 0 {
		t.Fatalf("清空后白名单应为空: %v", updated.IpWhitelist)
	}

	// 越权拒绝
	if _, err := svc.SetSupplierIPWhitelist(userCtx(2), &storefrontv1.SetSupplierIPWhitelistRequest{
		Id: acc.Id, Ips: []string{"1.2.3.4"},
	}); err == nil {
		t.Fatal("越权设置白名单应拒绝")
	}

	// 未审核账户拒绝
	acc2, _ := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "未审核",
	})
	if _, err := svc.SetSupplierIPWhitelist(userCtx(1), &storefrontv1.SetSupplierIPWhitelistRequest{
		Id: acc2.Id, Ips: []string{"1.2.3.4"},
	}); err == nil {
		t.Fatal("未审核账户应拒绝设置白名单")
	}
}
