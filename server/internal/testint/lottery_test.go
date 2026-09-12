//go:build integration

package testint

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotterydraw"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/lottery"
	"sync"
	"testing"
	"time"
)

func TestLotteryMySQL(t *testing.T) { runLottery(MySQL(t)) }
func TestLotteryPG(t *testing.T)    { runLottery(PG(t)) }
func runLottery(h *Harness) {
	t := h.T
	ctx := context.Background()
	c := h.Data.Client
	cipher, e := inventory.NewCardCipher(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	r := lottery.NewRepo(h.Data, cipher)
	s := lottery.NewAdminService(r)
	u := c.User.Create().SetUsername("lottery-member").SetPasswordHash("hash").SaveX(ctx)
	cfg := &adminv1.LotteryActivity{Name: "并发抽奖", StartAt: time.Now().Add(-time.Hour).Unix(), EndAt: time.Now().Add(time.Hour).Unix(), Timezone: "Asia/Shanghai", ChanceMode: "once", ChanceCount: 100, Prizes: []*adminv1.LotteryPrize{{Name: "文本奖", Mode: "text", Content: "私密结果", Quantity: 100, Probability: 10000}}}
	a, e := s.SaveLotteryActivity(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	a, e = s.SetLotteryStatus(ctx, &adminv1.LotteryStatusRequest{Id: a.Id, Revision: a.Revision, Status: "live"})
	if e != nil {
		t.Fatal(e)
	}
	// Real concurrent independent DB transactions, one request must debit/issue exactly once.
	var wg sync.WaitGroup
	errs := make([]error, 12)
	nos := make([]string, 12)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, e := r.Draw(ctx, a.Id, u.ID, "same-request-123456")
			errs[i] = e
			if d != nil {
				nos[i] = d.DrawNo
			}
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil || nos[i] != nos[0] {
			t.Fatalf("idempotency %d: %s %v", i, nos[i], e)
		}
	}
	if c.LotteryDraw.Query().CountX(ctx) != 1 || c.LotteryAccount.Query().OnlyX(ctx).Balance != 99 {
		t.Fatal("duplicate draw debit")
	}
	// Race the same physical inventory against the actual checkout reservation path.
	p := c.Product.Create().SetName("共享商品").SetSlug("lottery-race").SetStatus(1).SaveX(ctx)
	for i := 0; i < 20; i++ {
		sealed, _ := cipher.Seal(fmt.Sprintf("card-%d", i), p.ID, 0)
		c.Card.Create().SetProductID(p.ID).SetContent(sealed).SetContentHash(fmt.Sprint(i)).SaveX(ctx)
	}
	cfg.Id = 0
	cfg.Prizes = []*adminv1.LotteryPrize{{Name: "卡密奖", Mode: "card", ProductId: p.ID, Quantity: 100, Probability: 10000}}
	a, e = s.SaveLotteryActivity(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	a, e = s.SetLotteryStatus(ctx, &adminv1.LotteryStatusRequest{Id: a.Id, Revision: a.Revision, Status: "live"})
	if e != nil {
		t.Fatal(e)
	}
	// Start with one known success so receipts remain part of the contention assertion.
	if _, e = r.Draw(ctx, a.Id, u.ID, "first-card-123456"); e != nil {
		t.Fatal(e)
	}
	inv := inventory.NewCardRepoImpl(h.Data, cipher)
	start := make(chan struct{})
	errs = make([]error, 40)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				_, errs[i] = r.Draw(ctx, a.Id, u.ID, fmt.Sprintf("request-race-%08d", i))
			} else {
				errs[i] = data.Tx(ctx, h.Data, func(ctx context.Context) error {
					_, e := inv.Reserve(ctx, 0, []port.ReserveItem{{ProductID: p.ID, Quantity: 1}})
					return e
				})
			}
		}(i)
	}
	close(start)
	wg.Wait()
	draws := c.LotteryDraw.Query().Where(lotterydraw.ActivityID(a.Id)).AllX(ctx)
	used := c.Card.Query().Where(card.ProductID(p.ID), card.StatusEQ(card.StatusUsed)).CountX(ctx)
	reserved := c.Card.Query().Where(card.ProductID(p.ID), card.StatusEQ(card.StatusReserved)).CountX(ctx)
	available := c.Card.Query().Where(card.ProductID(p.ID), card.StatusEQ(card.StatusAvailable)).CountX(ctx)
	if used != len(draws) || used+reserved+available != 20 {
		t.Fatalf("stock mismatch awards=%d used=%d reserved=%d available=%d", len(draws), used, reserved, available)
	}
	for _, d := range draws {
		if d.CardID == nil || c.Card.GetX(ctx, *d.CardID).Status != card.StatusUsed {
			t.Fatal("awarded card also reserved")
		}
		if _, e := r.Content(ctx, d); e != nil {
			t.Fatal(e)
		}
	}
	if c.Order.Query().CountX(ctx) != 0 || c.Payment.Query().CountX(ctx) != 0 || c.WalletTransaction.Query().CountX(ctx) != 0 {
		t.Fatal("draw touched commerce ledger")
	}
	// Receipt survives a new repository (process restart) and exhausted inventory.
	fresh := lottery.NewRepo(h.Data, cipher)
	again, e := fresh.Draw(ctx, a.Id, u.ID, "first-card-123456")
	if e != nil || again.CardID == nil {
		t.Fatal("recovery failed", e)
	}
	t.Logf("concurrent draw/checkout: awards=%d reservations=%d available=%d", used, reserved, available)
}
