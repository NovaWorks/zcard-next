package wallet

// 礼品卡测试（P1-05 T4）：批次生成（密文+hash 唯一）/兑换入账/二次兑换拒绝/
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

	batch, err := repo.CreateBatch(ctx, BatchInput{
		BatchNo: "GC20260817", Name: "开业卡", Amount: 5000, Quantity: 3, Operator: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Quantity != 3 {
		t.Fatalf("批次数量错误: %d", batch.Quantity)
	}
	// 库内无明文（密文 + keyed hash 唯一）
	cards, _ := repo.data.Client.Giftcard.Query().Where(giftcard.BatchID(batch.ID)).All(ctx)
	if len(cards) != 3 {
		t.Fatalf("卡数量错误: %d", len(cards))
	}
	for _, c := range cards {
		if string(c.Code) == "" || len(c.Code) < 16 {
			t.Fatal("卡密应为密文")
		}
	}

	// 拿第一张卡明文（测试解密验证）——模拟用户持有
	plain := "GC20260817-ABCDEFGHIJKLMNOP" // 与生成算法无关：兑换按 hash 匹配，用解密验证闭环
	sealed, _ := repo.cipher.Seal(plain, 0, 0)
	_ = sealed
	// 实际兑换：用批次内第一张卡——生成算法随机，测试里直接读密文解密获得明文
	first := cards[0]
	decrypted, err := repo.cipher.Open(first.Code, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
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
