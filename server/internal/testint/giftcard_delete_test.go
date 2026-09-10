//go:build integration

package testint

import (
	"context"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/giftcard"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
)

func TestGiftcardDeleteMySQL(t *testing.T) { runGiftcardDelete(MySQL(t)) }
func TestGiftcardDeletePG(t *testing.T)    { runGiftcardDelete(PG(t)) }

func runGiftcardDelete(h *Harness) {
	t := h.T
	ctx := context.Background()
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	w := wallet.NewWalletRepoImpl(h.Data)
	r := wallet.NewGiftcardRepo(h.Data, cipher, w)
	for i := 0; i < 10; i++ {
		batch, codes, err := r.CreateBatch(ctx, wallet.BatchInput{BatchNo: fmt.Sprintf("DELETE-%d", i), Name: "并发测试", Amount: 100, Quantity: 1})
		if err != nil {
			t.Fatal(err)
		}
		userID := uint64(i + 1)
		start := make(chan struct{})
		deleted := make(chan error, 1)
		redeemed := make(chan error, 1)
		go func() { <-start; deleted <- r.DeleteBatch(ctx, batch.ID) }()
		go func() { <-start; _, err := r.Redeem(ctx, codes[0], userID); redeemed <- err }()
		close(start)
		if err := <-deleted; err != nil {
			t.Fatal(err)
		}
		redeemErr := <-redeemed
		card, err := h.Data.Client.Giftcard.Query().Where(giftcard.BatchID(batch.ID)).Only(ctx)
		if err != nil {
			t.Fatal(err)
		}
		balance, _, err := w.GetBalance(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		if redeemErr == nil {
			if card.Status != giftcard.StatusUsed || balance != 100 {
				t.Fatalf("兑换成功却未保留入账: %s %d", card.Status, balance)
			}
		} else {
			if card.Status != giftcard.StatusDisabled || balance != 0 {
				t.Fatalf("作废后错误入账: %s %d %v", card.Status, balance, redeemErr)
			}
		}
		if _, err := r.Redeem(ctx, codes[0], userID+100); err == nil {
			t.Fatal("删除后仍可兑换")
		}
	}
	_, total, err := r.ListBatches(ctx, 1, 20)
	if err != nil || total != 0 {
		t.Fatalf("删除后列表: %d %v", total, err)
	}
}
