package supplier

// 前台对接申请链路：申请 → 审核（通过/驳回）→ 凭据查看/重置 → 撤销；
// 属主越权拒绝、防刷护栏、凭据格式兼容三面板。

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplieraccount"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"google.golang.org/protobuf/types/known/emptypb"
)

func newStoreSupplierService(t *testing.T) (*StoreSupplierService, *SupplierRepoImpl) {
	t.Helper()
	repo, _ := newSupplierTestData(t)
	return NewStoreSupplierService(repo), repo
}

func userCtx(uid uint64) context.Context {
	return identity.WithClaims(context.Background(), &authn.Claims{Subject: uid, Realm: authn.RealmUser})
}

func TestStoreSubmitApplication(t *testing.T) {
	svc, _ := newStoreSupplierService(t)
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
	// 回调必须 HTTPS
	if _, err := svc.SubmitSupplierApplication(userCtx(1), &storefrontv1.SubmitSupplierApplicationRequest{
		Protocol: "zcard", DisplayName: "x", NotifyUrl: "http://plain.example.com/cb",
	}); err == nil {
		t.Fatal("非 HTTPS 回调应拒绝")
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
