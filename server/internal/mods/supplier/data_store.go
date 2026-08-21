package supplier

// 前台对接申请（个人中心）：提交申请 → 后台审核 → 凭据管理。
// 凭据格式兼容三面板：api_key=16 字节 hex（32 字符，满足 acg-faka app_id ≤32 校验）、
// api_secret=32 字节 hex（64 字符，acg/dujiao/zcard 均放行）。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplieraccount"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	settingsport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	paymentport "github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// 防刷护栏：同一用户申请中 ≤5、账户总数 ≤10。
const (
	maxApplyingPerUser = 5
	maxAccountsPerUser = 10
)

// StoreSupplierService 前台供货对接服务。
type StoreSupplierService struct {
	storefrontv1.UnimplementedStoreSupplierServiceServer
	repo     *SupplierRepoImpl
	settings settingsport.SettingsReader // 充值档位（recharge 组；nil = 目录缺省）
	payer    paymentport.RechargePayer   // 充值支付单创建（nil = 支付管线未装配）
}

// NewStoreSupplierService 构造。
func NewStoreSupplierService(repo *SupplierRepoImpl, settings settingsport.SettingsReader, payer paymentport.RechargePayer) *StoreSupplierService {
	return &StoreSupplierService{repo: repo, settings: settings, payer: payer}
}

// currentUser 当前登录用户（user realm JWT；未登录 401）。
func currentUser(ctx context.Context) (uint64, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return 0, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	return claims.Subject, nil
}

// SubmitSupplierApplication 提交对接申请（服务端生成凭据；secret 不在此返回）。
func (s *StoreSupplierService) SubmitSupplierApplication(ctx context.Context, req *storefrontv1.SubmitSupplierApplicationRequest) (*storefrontv1.SupplierAccountReply, error) {
	uid, err := currentUser(ctx)
	if err != nil {
		return nil, err
	}
	protocol := strings.TrimSpace(req.GetProtocol())
	if protocol != "zcard" && protocol != "dujiao_next" && protocol != "acg_faka" {
		return nil, errors.BadRequest("supplier.INVALID_PROTOCOL", "协议必须为 zcard|dujiao_next|acg_faka")
	}
	displayName := strings.TrimSpace(req.GetDisplayName())
	if displayName == "" {
		return nil, errors.BadRequest("supplier.NAME_REQUIRED", "站点/店铺名不能为空")
	}
	if len([]rune(displayName)) > 100 {
		return nil, errors.BadRequest("supplier.NAME_TOO_LONG", "站点/店铺名不能超过 100 字")
	}
	if len([]rune(req.GetApplyReason())) > 500 {
		return nil, errors.BadRequest("supplier.REASON_TOO_LONG", "申请理由不能超过 500 字")
	}
	// 回调地址：http/https 均支持；裸域名自动补 https://（用户只需填域名）
	notifyURL := strings.TrimSpace(req.GetNotifyUrl())
	if notifyURL != "" {
		notifyURL = normalizeCallbackURL(notifyURL)
		u, err := url.Parse(notifyURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, errors.BadRequest("supplier.NOTIFY_URL_INVALID", "回调地址格式不正确（填域名即可，如 shop.example.com/callback）")
		}
	}
	// 防刷护栏（并发下轻微超限可接受——上限为软约束）
	applying, err := s.repo.CountMyApplying(ctx, uid)
	if err != nil {
		return nil, err
	}
	if applying >= maxApplyingPerUser {
		return nil, errors.Forbidden("supplier.APPLYING_LIMIT", "申请中的账户已达上限，请等待审核")
	}
	total, err := s.repo.CountMyAccounts(ctx, uid)
	if err != nil {
		return nil, err
	}
	if total >= maxAccountsPerUser {
		return nil, errors.Forbidden("supplier.ACCOUNT_LIMIT", "对接账户数量已达上限")
	}
	apiKey, apiSecret, err := generateCredentials()
	if err != nil {
		return nil, err
	}
	acc, err := s.repo.CreateApplication(ctx, uid, protocol, displayName,
		strings.TrimSpace(req.GetContact()), strings.TrimSpace(req.GetApplyReason()), notifyURL, apiKey, apiSecret)
	if err != nil {
		return nil, err
	}
	return toStoreAccountPB(acc), nil
}

// ListMySupplierAccounts 我的对接账户。
func (s *StoreSupplierService) ListMySupplierAccounts(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.ListSupplierAccountsReply, error) {
	uid, err := currentUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListMyAccounts(ctx, uid)
	if err != nil {
		return nil, err
	}
	reply := &storefrontv1.ListSupplierAccountsReply{}
	for _, r := range rows {
		reply.Accounts = append(reply.Accounts, toStoreAccountPB(r))
	}
	return reply, nil
}

// GetSupplierCredentials 查看凭据（仅 approved 且归属本人；返回明文 secret）。
func (s *StoreSupplierService) GetSupplierCredentials(ctx context.Context, req *storefrontv1.GetSupplierCredentialsRequest) (*storefrontv1.SupplierCredentialsReply, error) {
	uid, err := currentUser(ctx)
	if err != nil {
		return nil, err
	}
	acc, err := s.mine(ctx, uid, req.GetId())
	if err != nil {
		return nil, err
	}
	if acc.Status != supplieraccount.StatusApproved {
		return nil, errors.Forbidden("supplier.NOT_APPROVED", "审核通过后才能查看凭据")
	}
	_, secret, err := s.repo.CredentialsOf(ctx, acc.ID)
	if err != nil {
		return nil, errors.InternalServer("supplier.SECRET_DECRYPT_FAILED", "密钥解密失败，请联系管理员重置")
	}
	return &storefrontv1.SupplierCredentialsReply{
		Id: acc.ID, Protocol: string(acc.Protocol), Status: string(acc.Status),
		ApiKey: acc.APIKey, ApiSecret: secret,
	}, nil
}

// RegenerateSupplierSecret 重置密钥（旧 secret 立即失效；新明文仅此一次）。
func (s *StoreSupplierService) RegenerateSupplierSecret(ctx context.Context, req *storefrontv1.RegenerateSupplierSecretRequest) (*storefrontv1.SupplierCredentialsReply, error) {
	uid, err := currentUser(ctx)
	if err != nil {
		return nil, err
	}
	acc, err := s.mine(ctx, uid, req.GetId())
	if err != nil {
		return nil, err
	}
	if acc.Status != supplieraccount.StatusApproved {
		return nil, errors.Forbidden("supplier.NOT_APPROVED", "审核通过后才能重置密钥")
	}
	_, newSecret, err := generateCredentials()
	if err != nil {
		return nil, err
	}
	if err := s.repo.ResetSecret(ctx, acc.ID, newSecret); err != nil {
		return nil, err
	}
	return &storefrontv1.SupplierCredentialsReply{
		Id: acc.ID, Protocol: string(acc.Protocol), Status: string(acc.Status),
		ApiKey: acc.APIKey, ApiSecret: newSecret,
	}, nil
}

// CancelSupplierApplication 撤销申请（仅 applying）。
func (s *StoreSupplierService) CancelSupplierApplication(ctx context.Context, req *storefrontv1.CancelSupplierApplicationRequest) (*emptypb.Empty, error) {
	uid, err := currentUser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.mine(ctx, uid, req.GetId()); err != nil {
		return nil, err
	}
	if err := s.repo.CancelApplication(ctx, req.GetId()); err != nil {
		return nil, errors.BadRequest("supplier.CANCEL_NOT_ALLOWED", err.Error())
	}
	return &emptypb.Empty{}, nil
}

// CreateSupplierRecharge 对接账户自助充值（金额服务端档位裁决；支付确认前不入账）。
//
// 安全口径：amount_cents 只是意向，服务端按 settings.supplier_recharge 档位
// （enabled/min_amount/max_amount——供货充值独立配置组，与钱包 recharge 组隔离）
// 校验；落 recharge_orders (pending, target=supply)。余额入账只发生在支付回调
// 成功后（payment.succeeded → settleRecharge 按 target 分支入账供货账户，
// reference=recharge:<paymentID> 幂等）。供货预存无赠送（gift=0）。
func (s *StoreSupplierService) CreateSupplierRecharge(ctx context.Context, req *storefrontv1.CreateSupplierRechargeRequest) (*storefrontv1.CreateSupplierRechargeReply, error) {
	uid, err := currentUser(ctx)
	if err != nil {
		return nil, err
	}
	acc, err := s.mine(ctx, uid, req.GetId())
	if err != nil {
		return nil, err
	}
	if acc.Status != supplieraccount.StatusApproved {
		return nil, errors.Forbidden("supplier.NOT_APPROVED", "审核通过后才能充值")
	}
	amount := req.GetAmountCents()
	minAmount, maxAmount, enabled := s.rechargePolicy(ctx)
	if !enabled {
		return nil, errors.Forbidden("supplier.RECHARGE_DISABLED", "充值功能未开放")
	}
	if !money.ValidCents(amount) || amount < minAmount || amount > maxAmount {
		return nil, errors.BadRequest("supplier.INVALID_AMOUNT", "充值金额超出允许范围")
	}
	if s.payer == nil {
		return nil, errors.InternalServer("supplier.PAYMENT_UNBOUND", "支付管线未装配")
	}
	ro, err := s.repo.CreateSupplyRechargeOrder(ctx, uid, acc.ID, amount)
	if err != nil {
		return nil, errors.InternalServer("supplier.RECHARGE_FAILED", "创建充值单失败")
	}
	info, err := s.payer.CreateRechargePayment(ctx, ro.ID, req.GetChannel(), money.Cents(amount))
	if err != nil {
		return nil, mapSupplierRechargeErr(err)
	}
	return &storefrontv1.CreateSupplierRechargeReply{
		RechargeId: ro.ID, PaymentId: info.PaymentID,
		Type: info.Type, Payload: info.Payload,
	}, nil
}

// rechargePolicy 供货充值档位（settings.supplier_recharge 组——独立于钱包
// recharge 组；缺省 min=1000 分 / max=500000 分 / enabled=true，与目录一致）。
func (s *StoreSupplierService) rechargePolicy(ctx context.Context) (minAmount, maxAmount int64, enabled bool) {
	minAmount, maxAmount, enabled = 1000, 500000, true
	if s.settings == nil {
		return
	}
	get := func(key string, out any) bool {
		raw, err := s.settings.GetJSON(ctx, "supplier_recharge", key)
		if err != nil || len(raw) == 0 {
			return false
		}
		return json.Unmarshal(raw, out) == nil
	}
	get("enabled", &enabled)
	get("min_amount", &minAmount)
	get("max_amount", &maxAmount)
	return
}

// mapSupplierRechargeErr 充值支付发起错误映射（与钱包 mapRechargeErr 同口径）。
func mapSupplierRechargeErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, prefix := range []string{"payment.CHANNEL_NOT_FOUND", "payment.CHANNEL_DISABLED", "payment.CONFIG_INVALID"} {
		if strings.HasPrefix(msg, prefix) {
			return errors.BadRequest("supplier.PAYMENT_CHANNEL_UNAVAILABLE", "支付渠道不可用，请更换支付方式")
		}
	}
	return errors.InternalServer("supplier.PAYMENT_FAILED", "发起支付失败，请稍后重试")
}

// mine 属主校验：账户必须归属当前用户。
func (s *StoreSupplierService) mine(ctx context.Context, uid, id uint64) (*ent.SupplierAccount, error) {
	acc, err := s.repo.GetAccount(ctx, id)
	if err != nil {
		if err == ErrNotFound {
			return nil, errors.NotFound("supplier.NOT_FOUND", "账户不存在")
		}
		return nil, err
	}
	if acc.OwnerUserID != uid {
		return nil, errors.NotFound("supplier.NOT_FOUND", "账户不存在")
	}
	return acc, nil
}

// normalizeCallbackURL 裸域名补 https://（用户只填域名场景）；已带 http/https
// 前缀原样返回；带其他 scheme（ftp:// 等）不处理——交由调用方格式校验拒绝。
func normalizeCallbackURL(raw string) string {
	if strings.Contains(raw, "://") {
		return raw
	}
	return "https://" + raw
}

// SetSupplierIPWhitelist 设置 IP 白名单（仅 approved 且归属本人；空 = 所有 IP 放行）。
func (s *StoreSupplierService) SetSupplierIPWhitelist(ctx context.Context, req *storefrontv1.SetSupplierIPWhitelistRequest) (*storefrontv1.SupplierAccountReply, error) {
	uid, err := currentUser(ctx)
	if err != nil {
		return nil, err
	}
	acc, err := s.mine(ctx, uid, req.GetId())
	if err != nil {
		return nil, err
	}
	if acc.Status != supplieraccount.StatusApproved {
		return nil, errors.Forbidden("supplier.NOT_APPROVED", "审核通过后才能配置 IP 白名单")
	}
	ips, err := normalizeWhitelist(req.GetIps())
	if err != nil {
		return nil, errors.BadRequest("supplier.IP_WHITELIST_INVALID", err.Error())
	}
	if err := s.repo.SetIPWhitelist(ctx, acc.ID, ips); err != nil {
		return nil, err
	}
	updated, err := s.repo.GetAccount(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	return toStoreAccountPB(updated), nil
}

// normalizeWhitelist 白名单条目清洗：去空/去重 + 格式校验（精确 IP 或 CIDR）+ 数量上限。
func normalizeWhitelist(ips []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" || seen[ip] {
			continue
		}
		if err := ValidateIPWhitelistEntry(ip); err != nil {
			return nil, err
		}
		seen[ip] = true
		out = append(out, ip)
	}
	if len(out) > maxWhitelistEntries {
		return nil, fmt.Errorf("白名单最多 %d 条", maxWhitelistEntries)
	}
	return out, nil
}

// generateCredentials 生成一对凭据（api_key 32 字符 hex / api_secret 64 字符 hex）。
func generateCredentials() (apiKey, apiSecret string, err error) {
	keyBytes := make([]byte, 16)
	if _, err = rand.Read(keyBytes); err != nil {
		return "", "", err
	}
	secretBytes := make([]byte, 32)
	if _, err = rand.Read(secretBytes); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(keyBytes), hex.EncodeToString(secretBytes), nil
}

// toStoreAccountPB 前台账户视图（secret 永不出现；审核通过后走凭据接口）。
func toStoreAccountPB(acc *ent.SupplierAccount) *storefrontv1.SupplierAccountReply {
	p := &storefrontv1.SupplierAccountReply{
		Id: acc.ID, Protocol: string(acc.Protocol), Status: string(acc.Status),
		DisplayName: acc.DisplayName, Contact: acc.Contact,
		ApplyReason: acc.ApplyReason, ReviewNote: acc.ReviewNote,
		ApiKey: acc.APIKey, CreatedAt: acc.CreatedAt.Unix(),
		BalanceCache: acc.BalanceCache, IpWhitelist: acc.IPWhitelist,
	}
	if !acc.ReviewedAt.IsZero() {
		p.ReviewedAt = acc.ReviewedAt.Unix()
	}
	return p
}
