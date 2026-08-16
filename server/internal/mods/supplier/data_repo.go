package supplier

// 供货仓储（P2-03）：下游账户（secret AES-GCM）、供货账本（幂等键 append-only）、
// 供货订单、nonce 防重放、差异化定价、回调记录。

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/downstreamcallback"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplieraccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplierledgerentry"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplierproductprice"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplynonce"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyorder"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// 哨兵错误。
var (
	ErrNotFound          = errors.New("supplier: 记录不存在")
	ErrInsufficientBalance = errors.New("supplier: 供货余额不足")
	ErrDuplicateLedger   = errors.New("supplier: 账本幂等键已存在")
)

// secretAAD api_secret 加密 AAD（按账户 api_key 绑定）。
func secretAAD(apiKey string) []byte { return []byte("supplier_account:" + apiKey) }

// SupplierRepoImpl 供货仓储。
type SupplierRepoImpl struct {
	data *data.Data
	box  *crypto.Box // ZCARD_DATA_KEY
}

// NewSupplierRepoImpl 构造。
func NewSupplierRepoImpl(d *data.Data, box *crypto.Box) *SupplierRepoImpl {
	return &SupplierRepoImpl{data: d, box: box}
}

// ── 账户 ──────────────────────────────────────────────────

// CreateAccount 下游申请（secret 加密入库；只显示一次的语义由 admin service 处理）。
func (r *SupplierRepoImpl) CreateAccount(ctx context.Context, name, apiKey, apiSecret, contact string) (*ent.SupplierAccount, error) {
	enc, err := r.box.Seal([]byte(apiSecret), secretAAD(apiKey))
	if err != nil {
		return nil, fmt.Errorf("supplier: secret 加密失败: %w", err)
	}
	return data.Client(ctx, r.data).SupplierAccount.Create().
		SetName(name).
		SetAPIKey(apiKey).
		SetAPISecret(enc).
		SetContact(contact).
		SetStatus(supplieraccount.StatusApplying).
		Save(ctx)
}

// AccountByKey 按 key 查账户（AuthStore 实现；返回解密 secret）。
func (r *SupplierRepoImpl) AccountByKey(ctx context.Context, key string) (*ent.SupplierAccount, string, error) {
	acc, err := data.Client(ctx, r.data).SupplierAccount.Query().
		Where(supplieraccount.APIKey(key)).
		Only(ctx)
	if err != nil {
		return nil, "", err
	}
	plain, err := r.box.Open(acc.APISecret, secretAAD(acc.APIKey))
	if err != nil {
		return nil, "", fmt.Errorf("supplier: secret 解密失败（需重置密钥）: %w", err)
	}
	return acc, string(plain), nil
}

// GetAccount 账户详情（不含 secret）。
func (r *SupplierRepoImpl) GetAccount(ctx context.Context, id uint64) (*ent.SupplierAccount, error) {
	acc, err := data.Client(ctx, r.data).SupplierAccount.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return acc, nil
}

// ListAccounts 账户列表。
func (r *SupplierRepoImpl) ListAccounts(ctx context.Context, page, pageSize int) ([]*ent.SupplierAccount, int, error) {
	q := data.Client(ctx, r.data).SupplierAccount.Query().Order(ent.Desc(supplieraccount.FieldID))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return rows, total, err
}

// ReviewAccount 审核（applying → approved/disabled）。
func (r *SupplierRepoImpl) ReviewAccount(ctx context.Context, id uint64, approve bool) (*ent.SupplierAccount, error) {
	status := supplieraccount.StatusDisabled
	if approve {
		status = supplieraccount.StatusApproved
	}
	acc, err := data.Client(ctx, r.data).SupplierAccount.UpdateOneID(id).
		SetStatus(status).
		SetReviewedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return acc, nil
}

// ToggleAccount 启停（approved ↔ disabled）。
func (r *SupplierRepoImpl) ToggleAccount(ctx context.Context, id uint64, enabled bool) (*ent.SupplierAccount, error) {
	status := supplieraccount.StatusDisabled
	if enabled {
		status = supplieraccount.StatusApproved
	}
	return data.Client(ctx, r.data).SupplierAccount.UpdateOneID(id).SetStatus(status).Save(ctx)
}

// ResetSecret 重置密钥（返回明文一次）。
func (r *SupplierRepoImpl) ResetSecret(ctx context.Context, id uint64, newSecret string) error {
	acc, err := r.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	enc, err := r.box.Seal([]byte(newSecret), secretAAD(acc.APIKey))
	if err != nil {
		return err
	}
	_, err = data.Client(ctx, r.data).SupplierAccount.UpdateOneID(id).SetAPISecret(enc).Save(ctx)
	return err
}

// SetNotifyURL 配置回调地址（HTTPS 校验在 service 层）。
func (r *SupplierRepoImpl) SetNotifyURL(ctx context.Context, id uint64, notifyURL string) error {
	_, err := data.Client(ctx, r.data).SupplierAccount.UpdateOneID(id).SetNotifyURL(notifyURL).Save(ctx)
	return err
}

// ── 账本（T4：append-only + 幂等键 + balance_cache）───────

// LedgerEntry 账本流水。
func (r *SupplierRepoImpl) LedgerEntry(ctx context.Context, accountID, supplyOrderID uint64, typ string, amount int64, reference, remark string) error {
	if amount == 0 {
		return fmt.Errorf("supplier: 流水金额不能为 0")
	}
	client := data.Client(ctx, r.data)
	// 余额预校验（扣款时）：不足拒绝且不产生流水（验收：余额不足拒绝且零流水）
	acc, err := client.SupplierAccount.Get(ctx, accountID)
	if err != nil {
		return err
	}
	if amount < 0 && acc.BalanceCache+amount < 0 {
		return ErrInsufficientBalance
	}
	// 幂等：reference UNIQUE（重复入账拒绝——重放安全）
	_, err = client.SupplierLedgerEntry.Create().
		SetAccountID(accountID).
		SetSupplyOrderID(supplyOrderID).
		SetType(typ).
		SetAmount(amount).
		SetReference(reference).
		SetRemark(remark).
		Save(ctx)
	if ent.IsConstraintError(err) {
		return ErrDuplicateLedger
	}
	if err != nil {
		return err
	}
	// balance_cache 更新（读-改-写；append-only 流水 + 可重算缓存，对账由流水重算）
	_, err = client.SupplierAccount.UpdateOneID(accountID).
		SetBalanceCache(acc.BalanceCache + amount).
		Save(ctx)
	return err
}

// BalanceOf 账户余额（balance_cache 快照；对账由流水重算）。
func (r *SupplierRepoImpl) BalanceOf(ctx context.Context, accountID uint64) (int64, error) {
	acc, err := r.GetAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	return acc.BalanceCache, nil
}

// ListLedger 账本流水（分页）。
func (r *SupplierRepoImpl) ListLedger(ctx context.Context, accountID uint64, page, pageSize int) ([]*ent.SupplierLedgerEntry, int, error) {
	q := data.Client(ctx, r.data).SupplierLedgerEntry.Query().
		Order(ent.Desc(supplierledgerentry.FieldCreatedAt))
	if accountID > 0 {
		q = q.Where(supplierledgerentry.AccountID(accountID))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return rows, total, err
}

// Recharge 充值（走 payment 管线的落点；本方法为账本原子入账）。
func (r *SupplierRepoImpl) Recharge(ctx context.Context, accountID uint64, amount int64, reference, remark string) error {
	return r.LedgerEntry(ctx, accountID, 0, "recharge", amount, reference, remark)
}

// ── nonce（AuthStore 实现）────────────────────────────────

// ConsumeNonce 消费 nonce（UNIQUE(key,nonce) 冲突 = 重放）。
func (r *SupplierRepoImpl) ConsumeNonce(ctx context.Context, key, nonce string, expiresAt time.Time) error {
	_, err := data.Client(ctx, r.data).SupplyNonce.Create().
		SetKey(key).
		SetNonce(nonce).
		SetExpiresAt(expiresAt).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return fmt.Errorf("supplier: nonce 重放")
		}
		return err
	}
	return nil
}

// CleanupExpiredNonces 过期 nonce 清理（cron）。
func (r *SupplierRepoImpl) CleanupExpiredNonces(ctx context.Context) error {
	_, err := data.Client(ctx, r.data).SupplyNonce.Delete().
		Where(supplynonce.ExpiresAtLT(time.Now().UTC())).
		Exec(ctx)
	return err
}

// ── 供货订单 ──────────────────────────────────────────────

// CreateSupplyOrder 创建供货订单（downstream_order_no 幂等）。
func (r *SupplierRepoImpl) CreateSupplyOrder(ctx context.Context, accountID uint64, downstreamOrderNo string, items []map[string]any, amount int64) (*ent.SupplyOrder, error) {
	o, err := data.Client(ctx, r.data).SupplyOrder.Create().
		SetAccountID(accountID).
		SetDownstreamOrderNo(downstreamOrderNo).
		SetItems(items).
		SetAmount(amount).
		SetStatus(supplyorder.StatusPending).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, fmt.Errorf("supplier: 重复 downstream_order_no")
		}
		return nil, err
	}
	return o, nil
}

// GetSupplyOrderByNo 按下游单号查（幂等返回首单）。
func (r *SupplierRepoImpl) GetSupplyOrderByNo(ctx context.Context, downstreamOrderNo string) (*ent.SupplyOrder, error) {
	o, err := data.Client(ctx, r.data).SupplyOrder.Query().
		Where(supplyorder.DownstreamOrderNo(downstreamOrderNo)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return o, nil
}

// GetSupplyOrder 按 id 查。
func (r *SupplierRepoImpl) GetSupplyOrder(ctx context.Context, id uint64) (*ent.SupplyOrder, error) {
	o, err := data.Client(ctx, r.data).SupplyOrder.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return o, nil
}

// MarkSupplyOrderFulfilled 交付完成（fulfilling → fulfilled + fulfilled_at）。
func (r *SupplierRepoImpl) MarkSupplyOrderFulfilled(ctx context.Context, id uint64) error {
	_, err := data.Client(ctx, r.data).SupplyOrder.UpdateOneID(id).
		SetStatus(supplyorder.StatusFulfilled).
		SetFulfilledAt(time.Now().UTC()).
		Save(ctx)
	return err
}

// MarkSupplyOrderPaid 扣款成功（pending → paid + paid_at）。
func (r *SupplierRepoImpl) MarkSupplyOrderPaid(ctx context.Context, id uint64) error {
	_, err := data.Client(ctx, r.data).SupplyOrder.UpdateOneID(id).
		SetStatus(supplyorder.StatusPaid).
		SetPaidAt(time.Now().UTC()).
		Save(ctx)
	return err
}

// MarkSupplyOrderRejected 下单拒绝（库存不足等）。
func (r *SupplierRepoImpl) MarkSupplyOrderRejected(ctx context.Context, id uint64) error {
	_, err := data.Client(ctx, r.data).SupplyOrder.UpdateOneID(id).
		SetStatus(supplyorder.StatusRejected).
		Save(ctx)
	return err
}

// ── 差异化定价 ────────────────────────────────────────────

// UpsertPrice 设置/更新供货价（UNIQUE(account, product, sku)）。
func (r *SupplierRepoImpl) UpsertPrice(ctx context.Context, accountID, productID, skuID uint64, price int64) (*ent.SupplierProductPrice, error) {
	client := data.Client(ctx, r.data)
	existing, err := client.SupplierProductPrice.Query().
		Where(
			supplierproductprice.SupplierAccountID(accountID),
			supplierproductprice.ProductID(productID),
			supplierproductprice.SkuID(skuID),
		).
		Only(ctx)
	if ent.IsNotFound(err) {
		return client.SupplierProductPrice.Create().
			SetSupplierAccountID(accountID).
			SetProductID(productID).
			SetSkuID(skuID).
			SetPrice(price).
			Save(ctx)
	}
	if err != nil {
		return nil, err
	}
	return client.SupplierProductPrice.UpdateOneID(existing.ID).SetPrice(price).Save(ctx)
}

// PriceOf 取供货价（覆盖价；无覆盖返回 0）。
func (r *SupplierRepoImpl) PriceOf(ctx context.Context, accountID, productID, skuID uint64) (int64, error) {
	row, err := data.Client(ctx, r.data).SupplierProductPrice.Query().
		Where(
			supplierproductprice.SupplierAccountID(accountID),
			supplierproductprice.ProductID(productID),
			supplierproductprice.SkuID(skuID),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	return row.Price, nil
}

// ── 回调记录（T5）─────────────────────────────────────────

// CreateCallback 创建回调任务（supply_order_id UNIQUE）。
func (r *SupplierRepoImpl) CreateCallback(ctx context.Context, supplyOrderID, accountID uint64, downstreamOrderNo, callbackURL, traceID string) (*ent.DownstreamCallback, error) {
	cb, err := data.Client(ctx, r.data).DownstreamCallback.Create().
		SetSupplyOrderID(supplyOrderID).
		SetAccountID(accountID).
		SetDownstreamOrderNo(downstreamOrderNo).
		SetCallbackURL(callbackURL).
		SetTraceID(traceID).
		SetCallbackStatus(downstreamcallback.CallbackStatusPending).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			// 已存在（重复交付）→ 幂等返回
			return r.GetCallbackByOrder(ctx, supplyOrderID)
		}
		return nil, err
	}
	return cb, nil
}

// GetCallbackByOrder 按订单查回调。
func (r *SupplierRepoImpl) GetCallbackByOrder(ctx context.Context, supplyOrderID uint64) (*ent.DownstreamCallback, error) {
	cb, err := data.Client(ctx, r.data).DownstreamCallback.Query().
		Where(downstreamcallback.SupplyOrderID(supplyOrderID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return cb, nil
}

// ListCallbacks 回调记录列表。
func (r *SupplierRepoImpl) ListCallbacks(ctx context.Context, status string, page, pageSize int) ([]*ent.DownstreamCallback, int, error) {
	q := data.Client(ctx, r.data).DownstreamCallback.Query().Order(ent.Desc(downstreamcallback.FieldID))
	if status != "" {
		q = q.Where(downstreamcallback.CallbackStatusEQ(downstreamcallback.CallbackStatus(status)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return rows, total, err
}

// ListPendingCallbacks 待发回调（重试轮询）。
func (r *SupplierRepoImpl) ListPendingCallbacks(ctx context.Context, limit int) ([]*ent.DownstreamCallback, error) {
	return data.Client(ctx, r.data).DownstreamCallback.Query().
		Where(
			downstreamcallback.CallbackStatusIn(
				downstreamcallback.CallbackStatusPending,
				downstreamcallback.CallbackStatusFailed,
			),
		).
		Order(ent.Asc(downstreamcallback.FieldID)).
		Limit(limit).
		All(ctx)
}

// MarkCallbackResult 回调结果（success/failed + 退避计数）。
func (r *SupplierRepoImpl) MarkCallbackResult(ctx context.Context, id uint64, success bool, errMsg string) error {
	cb, err := data.Client(ctx, r.data).DownstreamCallback.Get(ctx, id)
	if err != nil {
		return err
	}
	upd := data.Client(ctx, r.data).DownstreamCallback.UpdateOneID(id).
		SetLastCallbackAt(time.Now().UTC()).
		SetRetryCount(cb.RetryCount + 1)
	if success {
		upd.SetCallbackStatus(downstreamcallback.CallbackStatusSuccess)
	} else {
		upd.SetCallbackStatus(downstreamcallback.CallbackStatusFailed).SetLastError(errMsg)
	}
	_, err = upd.Save(ctx)
	return err
}

// ResetCallback 手动重发（failed → pending + 清计数）。
func (r *SupplierRepoImpl) ResetCallback(ctx context.Context, id uint64) (*ent.DownstreamCallback, error) {
	return data.Client(ctx, r.data).DownstreamCallback.UpdateOneID(id).
		SetCallbackStatus(downstreamcallback.CallbackStatusPending).
		SetRetryCount(0).
		ClearLastError().
		Save(ctx)
}

// CredentialsOf 按账户取凭据（api_key 明文 + 解密 secret；回调转发签名用）。
func (r *SupplierRepoImpl) CredentialsOf(ctx context.Context, accountID uint64) (apiKey, apiSecret string, err error) {
	acc, err := r.GetAccount(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	plain, err := r.box.Open(acc.APISecret, secretAAD(acc.APIKey))
	if err != nil {
		return "", "", fmt.Errorf("supplier: secret 解密失败（需重置密钥）: %w", err)
	}
	return acc.APIKey, string(plain), nil
}
