package license

// P3-08 M3：专业套餐在线购买（发行侧部署）。
//
// 模型：本部署同时充当发行侧时（settings.license.purchase_privkey 配置了 ed25519
// 签发私钥），站点用户可钱包余额购买专业套餐；签发的许可证绑定目标实例 ID
// （购买人填自己的实例 ID——在其部署后台可见；空=绑定本实例自装）。
// 原子性：购买单落库 + 钱包扣款 + 许可证签发自验 + （自购时）安装，全部同一事务；
// 任一步失败整单回滚（铁律 15/16：金额服务端裁决，扣款前零落账）。
// 经济闭环：充值管线（真实支付渠道）→ 钱包余额 → 购买扣款——「3U/月、30U/年」。

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/licenseorder"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	platformlicense "github.com/NovaWorks/zcard-next/server/internal/platform/license"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// 专业套餐特性清单（M3 首批；* 为永久通配——订阅版逐项授权）。
var professionalFeatures = []string{"analytics", "whitelabel_ads", "auto_pricing"}

// PurchaseOffer 报价视图。
type PurchaseOffer struct {
	MonthlyCents int64
	YearlyCents  int64
	Purchasable  bool   // 签发私钥已配置
	InstanceID   string // 本部署实例 ID
}

// PurchaseRepo 购买仓储。
type PurchaseRepo struct {
	data   *data.Data
	lic    *LicenseRepo
	wallet walletport.Wallet
}

// NewPurchaseRepo 构造。
func NewPurchaseRepo(d *data.Data, lic *LicenseRepo, wallet walletport.Wallet) *PurchaseRepo {
	return &PurchaseRepo{data: d, lic: lic, wallet: wallet}
}

// settingInt 读 license 组数值键（缺省回落默认值）。
func (r *PurchaseRepo) settingInt(ctx context.Context, key string, fallback int64) int64 {
	raw, err := r.lic.settings.Get(ctx, "license", key)
	if err != nil || len(raw) == 0 {
		return fallback
	}
	var v float64 // settings JSON 数值统一 float64
	if json.Unmarshal(raw, &v) != nil {
		return fallback
	}
	return int64(v)
}

// Offer 报价（价格服务端裁决；铁律 16）。
func (r *PurchaseRepo) Offer(ctx context.Context) (*PurchaseOffer, error) {
	self, err := r.lic.InstanceID(ctx)
	if err != nil {
		return nil, err
	}
	return &PurchaseOffer{
		MonthlyCents: r.settingInt(ctx, "purchase_monthly_cents", 300),
		YearlyCents:  r.settingInt(ctx, "purchase_yearly_cents", 3000),
		Purchasable:  r.issuerKey(ctx) != nil,
		InstanceID:   self,
	}, nil
}

// issuerKey 签发私钥（未配置返回 nil）。
func (r *PurchaseRepo) issuerKey(ctx context.Context) ed25519.PrivateKey {
	raw, err := r.lic.settings.Get(ctx, "license", "purchase_privkey")
	if err != nil || len(raw) == 0 {
		return nil
	}
	var b64 string
	if json.Unmarshal(raw, &b64) != nil || b64 == "" {
		return nil
	}
	priv, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return nil
	}
	return ed25519.PrivateKey(priv)
}

// Purchase 购买（单事务：建单 → 扣款 → 签发自验 → 自购安装 → success）。
func (r *PurchaseRepo) Purchase(ctx context.Context, payer uint64, plan, targetInstance, targetDomain string) (*ent.LicenseOrder, error) {
	// 事务前 fail-fast：档位/价格/密钥/公钥
	var price int64
	var months int
	switch plan {
	case "monthly":
		price, months = r.settingInt(ctx, "purchase_monthly_cents", 300), 1
	case "yearly":
		price, months = r.settingInt(ctx, "purchase_yearly_cents", 3000), 12
	default:
		return nil, fmt.Errorf("license.PLAN_INVALID")
	}
	if !money.ValidCents(price) || price <= 0 {
		return nil, fmt.Errorf("license.PRICE_INVALID")
	}
	priv := r.issuerKey(ctx)
	if priv == nil {
		return nil, fmt.Errorf("license.ISSUER_UNCONFIGURED")
	}
	pub, err := r.lic.PubKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("license.PUBKEY_UNCONFIGURED")
	}
	self, err := r.lic.InstanceID(ctx)
	if err != nil {
		return nil, err
	}
	if targetInstance == "" {
		targetInstance = self // 自购：绑定本实例
	}
	now := time.Now().UTC()
	expires := now.AddDate(0, months, 0)

	var row *ent.LicenseOrder
	err = data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)
		// 1) 购买单（pending）
		var derr error
		row, derr = client.LicenseOrder.Create().
			SetPlan(licenseorder.Plan(plan)).
			SetAmount(price).
			SetStatus(licenseorder.StatusPending).
			SetPayerUserID(payer).
			SetInstanceID(targetInstance).
			SetDomain(targetDomain).
			SetFeatures(professionalFeatures).
			SetExpiresAt(expires).
			Save(txCtx)
		if derr != nil {
			return derr
		}
		// 2) 钱包扣款（幂等键 license_order:<id>；余额不足整单回滚）
		if err := r.wallet.DebitInTx(txCtx, walletport.Entry{
			UserID: payer, Direction: walletport.DirectionOut,
			Type: "license_purchase", Amount: money.Cents(price),
			Reference: fmt.Sprintf("license_order:%d", row.ID),
			Remark:    "专业套餐 " + plan,
		}); err != nil {
			return fmt.Errorf("license.BALANCE_INSUFFICIENT: %w", err)
		}
		// 3) 签发 + 自验（防签发配置错误直接落库）
		file, err := platformlicense.Sign(priv, platformlicense.License{
			InstanceID: targetInstance, Domain: targetDomain,
			Features: professionalFeatures,
			IssuedAt: now.Format(time.RFC3339), ExpiresAt: expires.Format(time.RFC3339),
		})
		if err != nil {
			return err
		}
		if _, err := platformlicense.Verify(file, pub, targetInstance, targetDomain, now); err != nil {
			return fmt.Errorf("license.SIGN_VERIFY_FAILED: %w", err)
		}
		// 4) 自购（目标=本实例）：同事务安装直接激活
		if targetInstance == self {
			if err := r.lic.settings.Put(txCtx, "license", "file", json.RawMessage(`"`+jsonEscape(string(file))+`"`)); err != nil {
				return err
			}
		}
		// 5) 收尾 success
		row, err = client.LicenseOrder.UpdateOneID(row.ID).
			SetStatus(licenseorder.StatusSuccess).
			SetLicenseFile(string(file)).
			SetPaidAt(now).
			Save(txCtx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// ListPurchases 购买记录（购买人视角）。
func (r *PurchaseRepo) ListPurchases(ctx context.Context, payer uint64, page, size int) ([]*ent.LicenseOrder, int64, error) {
	q := data.Client(ctx, r.data).LicenseOrder.Query().
		Where(licenseorder.PayerUserID(payer)).
		Order(ent.Desc(licenseorder.FieldID))
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Clone().Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, int64(total), err
}
