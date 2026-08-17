package wallet

// P1-05 T4 礼品卡（M3，表 M1 建）：
//   批次创建（面额/数量/有效期）→ 批量生成 code（CardCipher 同款 AES-GCM 密文
//   + keyed hash 唯一索引）→ 兑换 = 查 hash → 核销 → CreditInTx(giftcard:<id>)。
// 防爆破：兑换失败计数（同用户 30s 窗口 5 次锁定）；库内无明文 code（铁律 11）。

import (
	"context"
	"fmt"
	"sync"
	"time"

	crand "crypto/rand"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/giftcard"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/giftcardbatch"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
)

// GiftcardRepo 礼品卡仓储（独立构造——code 加密走 CardCipher，兑换入账走 WalletRepoImpl）。
type GiftcardRepo struct {
	data   *data.Data
	cipher *inventory.CardCipher
	wallet *WalletRepoImpl
	// 防爆破限流（内存态：同用户失败计数，30s 窗口 5 次锁定）
	mu        sync.Mutex
	failures  map[uint64]int
	lockUntil map[uint64]time.Time
}

// NewGiftcardRepo 构造。
func NewGiftcardRepo(d *data.Data, cipher *inventory.CardCipher, wallet *WalletRepoImpl) *GiftcardRepo {
	return &GiftcardRepo{data: d, cipher: cipher, wallet: wallet, failures: map[uint64]int{}, lockUntil: map[uint64]time.Time{}}
}

// BatchInput 批次创建入参。
type BatchInput struct {
	BatchNo  string
	Name     string
	Amount   int64 // 面额（分）
	Quantity int32
	Operator uint64
}

// CreateBatch 批次创建 + 批量生成卡（密文 + keyed hash 唯一索引；批次号唯一幂等）。
func (r *GiftcardRepo) CreateBatch(ctx context.Context, in BatchInput) (*ent.GiftcardBatch, error) {
	// 批量生成（数量上限护栏：单批 ≤ 5000）
	if in.Quantity <= 0 || in.Quantity > 5000 {
		return nil, fmt.Errorf("giftcard.QUANTITY_INVALID")
	}
	var batch *ent.GiftcardBatch
	err := data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)
		created, err := client.GiftcardBatch.Create().
			SetBatchNo(in.BatchNo).
			SetName(in.Name).
			SetAmount(in.Amount).
			SetQuantity(in.Quantity).
			SetOperatorID(in.Operator).
			Save(txCtx)
		if err != nil {
			return fmt.Errorf("giftcard.BATCH_EXISTS: %w", err)
		}
		batch = created
		creates := make([]*ent.GiftcardCreate, 0, in.Quantity)
		for i := int32(0); i < in.Quantity; i++ {
			plain := randomGiftcardCode(in.BatchNo, i)
			sealed, err := r.cipher.Seal(plain, 0, 0)
			if err != nil {
				return err
			}
			creates = append(creates, client.Giftcard.Create().
				SetBatchID(batch.ID).
				SetCode(sealed).
				SetCodeHash(r.cipher.ContentHash(plain)).
				SetAmount(in.Amount).
				SetStatus(giftcard.StatusUnused))
		}
		if _, err := client.Giftcard.CreateBulk(creates...).Save(txCtx); err != nil {
			return fmt.Errorf("giftcard.BULK_FAILED: %w", err)
		}
		return nil
	})
	return batch, err
}

// ListBatches 批次列表。
func (r *GiftcardRepo) ListBatches(ctx context.Context, page, size int) ([]*ent.GiftcardBatch, int64, error) {
	q := data.Client(ctx, r.data).GiftcardBatch.Query().
		Order(ent.Desc(giftcardbatch.FieldID))
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, int64(total), err
}

// Redeem 兑换（登录用户）：查 hash → 核销 → 余额入账（幂等键 giftcard:<id>）。
// 失败（卡不存在/已用/过期）统一返回「卡密无效」（防枚举）；连续失败 5 次锁 30s。
func (r *GiftcardRepo) Redeem(ctx context.Context, code string, userID uint64) (int64, error) {
	// 防爆破：锁定窗口内直接拒绝
	r.mu.Lock()
	if until, ok := r.lockUntil[userID]; ok && time.Now().Before(until) {
		r.mu.Unlock()
		return 0, fmt.Errorf("giftcard.LOCKED: 尝试次数过多，请稍后再试")
	}
	r.mu.Unlock()

	client := data.Client(ctx, r.data)
	hash := r.cipher.ContentHash(code)
	g, err := client.Giftcard.Query().
		Where(giftcard.CodeHash(hash)).Only(ctx)
	if ent.IsNotFound(err) || g.Status != giftcard.StatusUnused {
		r.recordFailure(userID)
		return 0, fmt.Errorf("giftcard.INVALID: 卡密无效或已使用")
	}
	if err != nil {
		return 0, err
	}
	if !g.ExpiresAt.IsZero() && time.Now().After(g.ExpiresAt) {
		r.recordFailure(userID)
		return 0, fmt.Errorf("giftcard.INVALID: 卡密无效或已使用")
	}

	var credited int64
	err = data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)
		// 核销（CAS：unused → used；防并发重复兑换）
		affected, err := client.Giftcard.Update().
			Where(
				giftcard.ID(g.ID),
				giftcard.StatusEQ(giftcard.StatusUnused),
			).
			SetStatus(giftcard.StatusUsed).
			SetUsedBy(userID).
			SetUsedAt(time.Now().UTC()).
			Save(txCtx)
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("giftcard.INVALID: 卡密无效或已使用")
		}
		// 余额入账（幂等键 giftcard:<id>）
		if err := r.wallet.CreditInTx(txCtx, Entry{
			UserID: userID, Direction: "in", Type: "giftcard",
			Amount: g.Amount, Reference: fmt.Sprintf("giftcard:%d", g.ID),
			Remark: "礼品卡兑换",
		}); err != nil {
			return err
		}
		credited = g.Amount
		return nil
	})
	if err != nil {
		r.recordFailure(userID)
		return 0, err
	}
	r.clearFailures(userID)
	return credited, nil
}

// recordFailure 失败计数（30s 窗口 5 次 → 锁 30s）。
func (r *GiftcardRepo) recordFailure(userID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures[userID]++
	if r.failures[userID] >= 5 {
		r.lockUntil[userID] = time.Now().Add(30 * time.Second)
		r.failures[userID] = 0
	}
}

func (r *GiftcardRepo) clearFailures(userID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.failures, userID)
	delete(r.lockUntil, userID)
}

// randomGiftcardCode 高熵礼品卡码（批次号 + 序号 + 随机段 → 大写字母数字）。
func randomGiftcardCode(batchNo string, seq int32) string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 去易混淆字符
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return fmt.Sprintf("%s-%08d", batchNo, seq)
	}
	out := make([]byte, 20)
	for i, c := range b {
		out[i] = charset[int(c)%len(charset)]
	}
	return fmt.Sprintf("%s-%s", batchNo, string(out))
}
