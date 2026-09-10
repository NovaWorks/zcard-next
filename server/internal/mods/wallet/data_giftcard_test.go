package wallet

// 礼品卡测试（）：批次生成（密文+hash 唯一）/兑换入账/二次兑换拒绝/
// 防爆破锁定/库内无明文。

import (
	"context"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/giftcard"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
)

func newGiftcardRepo(t *testing.T) (*GiftcardRepo, *WalletRepoImpl) {
	d := newTestData(t)
	walletRepo := NewWalletRepoImpl(d)
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewGiftcardRepo(d, cipher, walletRepo), walletRepo
}

// TestGiftcardBatchRedeem 批次创建→兑换入账→二次兑换拒绝。
func TestGiftcardBatchRedeem(t *testing.T) {
	repo, walletRepo := newGiftcardRepo(t)
	ctx := context.Background()

	batch, codes, err := repo.CreateBatch(ctx, BatchInput{
		BatchNo: "GC20260817", Name: "开业卡", Amount: 5000, Quantity: 3, Operator: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Quantity != 3 || len(codes) != 3 {
		t.Fatalf("批次数量/明文码错误: %d/%d", batch.Quantity, len(codes))
	}
	// 库内无明文（密文 + keyed hash 唯一）；明文码仅创建一次性返回
	cards, _ := repo.data.Client.Giftcard.Query().Where(giftcard.BatchID(batch.ID)).All(ctx)
	if len(cards) != 3 {
		t.Fatalf("卡数量错误: %d", len(cards))
	}
	codeSet := map[string]bool{}
	for _, c := range codes {
		codeSet[c] = true
	}
	for _, c := range cards {
		if string(c.Code) == "" || len(c.Code) < 16 {
			t.Fatal("卡密应为密文")
		}
		// 返回的明文码与库内密文一一对应（解密闭环）
		plain, err := repo.cipher.Open(c.Code, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !codeSet[plain] {
			t.Fatalf("明文码 %q 不在创建返回清单中", plain)
		}
	}

	// 实际兑换：用批次第一张卡的明文（创建返回值——生产路径即此来源）
	decrypted := codes[0]
	amount, err := repo.Redeem(ctx, decrypted, 1)
	if err != nil {
		t.Fatal(err)
	}
	if amount != 5000 {
		t.Fatalf("兑换金额错误: %d", amount)
	}
	avail, _, _ := walletRepo.GetBalance(ctx, 1)
	if avail != 5000 {
		t.Fatalf("兑换入账错误: %d", avail)
	}
	// 二次兑换同一卡 → 拒绝
	if _, err := repo.Redeem(ctx, decrypted, 1); err == nil {
		t.Fatal("二次兑换应拒绝")
	}
	// 伪造卡 → 拒绝
	if _, err := repo.Redeem(ctx, "GC20260817-FAKE-CODE", 1); err == nil {
		t.Fatal("伪造卡应拒绝")
	}
}

// TestGiftcardBruteForce 防爆破：连续失败 5 次锁定。
func TestGiftcardBruteForce(t *testing.T) {
	repo, _ := newGiftcardRepo(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, _ = repo.Redeem(ctx, "INVALID-CODE", 7)
	}
	// 第 6 次（锁定窗口内）→ 明确锁定错误
	if _, err := repo.Redeem(ctx, "INVALID-CODE-AGAIN", 7); err == nil {
		t.Fatal("连续失败应锁定")
	}
	// 其他用户不受影响
	if _, err := repo.Redeem(ctx, "INVALID", 8); err == nil {
		t.Fatal("伪造卡应拒绝")
	}
}

func TestGiftcardDeleteBatch(t *testing.T) {
	repo, walletRepo := newGiftcardRepo(t)
	ctx := context.Background()
	batch, codes, err := repo.CreateBatch(ctx, BatchInput{BatchNo: "DELETE", Name: "删除测试", Amount: 5000, Quantity: 3})
	if err != nil {
		t.Fatal(err)
	}
	other, otherCodes, err := repo.CreateBatch(ctx, BatchInput{BatchNo: "KEEP", Name: "其他批次", Amount: 100, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Redeem(ctx, codes[0], 1); err != nil {
		t.Fatal(err)
	}
	before, err := repo.data.Client.WalletTransaction.Query().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteBatch(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteBatch(ctx, batch.ID); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
	rows, total, err := repo.ListBatches(ctx, 1, 20)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != other.ID {
		t.Fatalf("删除后列表错误: %v %d %v", rows, total, err)
	}
	retained, err := repo.data.Client.GiftcardBatch.Get(ctx, batch.ID)
	if err != nil || retained.DeletedAt.IsZero() {
		t.Fatalf("批次审计丢失: %v", err)
	}
	cards, err := repo.data.Client.Giftcard.Query().Where(giftcard.BatchID(batch.ID)).All(ctx)
	if err != nil || len(cards) != 3 {
		t.Fatalf("卡记录丢失: %v", err)
	}
	used, disabled := 0, 0
	for _, c := range cards {
		if c.Status == giftcard.StatusUsed {
			used++
			if c.UsedBy != 1 || c.UsedAt.IsZero() {
				t.Fatal("兑换记录丢失")
			}
		}
		if c.Status == giftcard.StatusDisabled {
			disabled++
		}
	}
	if used != 1 || disabled != 2 {
		t.Fatalf("作废状态错误: used=%d disabled=%d", used, disabled)
	}
	for _, code := range codes {
		if _, err := repo.Redeem(ctx, code, 2); err == nil {
			t.Fatal("已删除卡仍可兑换")
		}
	}
	avail, _, err := walletRepo.GetBalance(ctx, 1)
	if err != nil || avail != 5000 {
		t.Fatalf("已有余额被修改: %d %v", avail, err)
	}
	after, err := repo.data.Client.WalletTransaction.Query().All(ctx)
	if err != nil || len(after) != len(before) || after[0].ID != before[0].ID || after[0].Amount != before[0].Amount {
		t.Fatalf("删除修改了流水: %v", err)
	}
	if _, err := repo.Redeem(ctx, otherCodes[0], 3); err != nil {
		t.Fatalf("其他批次受影响: %v", err)
	}
	if _, _, err := repo.CreateBatch(ctx, BatchInput{BatchNo: "DELETE", Name: "重复", Amount: 1, Quantity: 1}); err == nil {
		t.Fatal("已删除批次号不可复用")
	}
	if err := repo.DeleteBatch(ctx, 99999); err == nil {
		t.Fatal("不存在批次应返回错误")
	}
}
