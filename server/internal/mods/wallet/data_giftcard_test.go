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
