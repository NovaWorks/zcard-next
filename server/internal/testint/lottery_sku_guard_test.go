//go:build integration

package testint

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryprize"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/lottery"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

func TestLotterySKUCurrentReadMySQL(t *testing.T) { runLotterySKUCurrentRead(MySQL(t)) }
func TestLotterySKUCurrentReadPG(t *testing.T)    { runLotterySKUCurrentRead(PG(t)) }

func runLotterySKUCurrentRead(h *Harness) {
	t, d := h.T, h.Data
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if h.D == db.MySQL {
		var isolation string
		if err := d.DB.QueryRowContext(ctx, "SELECT @@transaction_isolation").Scan(&isolation); err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(isolation, "REPEATABLE-READ") {
			t.Fatalf("stale snapshot test requires REPEATABLE READ, got %s", isolation)
		}
	}
	cipher, _ := inventory.NewCardCipher(make([]byte, 32))
	svc := lottery.NewAdminService(lottery.NewRepo(d, cipher))
	p := d.Client.Product.Create().SetName("prize").SetSlug("prize").SetStatus(1).SaveX(ctx)
	sk := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("monthly").SetSpecValues(map[string]string{}).SaveX(ctx)
	activity, err := svc.SaveLotteryActivity(ctx, &adminv1.LotteryActivity{
		Name: "draft", StartAt: time.Now().Add(-time.Hour).Unix(), EndAt: time.Now().Add(time.Hour).Unix(),
		Timezone: "UTC", ChanceMode: "once", ChanceCount: 1,
		Prizes: []*adminv1.LotteryPrize{{Name: "text", Mode: "text", Content: "prize", Quantity: 1, Probability: 10000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	activity.Prizes = []*adminv1.LotteryPrize{{Name: "card", Mode: "card", ProductId: p.ID, SkuId: sk.ID, Quantity: 1, Probability: 10000}}
	writerLocked, readerSnapshot, releaseWriter := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseWriter) })
	defer release()
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- data.Tx(ctx, d, func(tx context.Context) error {
			if err := catalog.NewProductRepoImpl(d, nil).DeleteSku(tx, sk.ID); err != nil {
				return err
			}
			close(writerLocked)
			select {
			case <-releaseWriter:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-writerLocked:
	case err := <-writerDone:
		t.Fatalf("deletion did not acquire product lock: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	readerDone := make(chan error, 1)
	go func() {
		readerDone <- data.Tx(ctx, d, func(tx context.Context) error {
			// Save can enter with a snapshot established by an earlier query,
			// including its own existing-activity read before the product lock.
			n, err := data.Client(tx, d).ProductSku.Query().Where(productsku.ID(sk.ID)).Count(tx)
			if err != nil || n != 1 {
				return fmt.Errorf("pre-deletion snapshot: count=%d err=%v", n, err)
			}
			close(readerSnapshot)
			_, err = svc.SaveLotteryActivity(tx, activity)
			return err
		})
	}()
	select {
	case <-readerSnapshot:
	case err := <-readerDone:
		t.Fatalf("save failed before establishing a snapshot: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-readerDone:
		t.Fatalf("save bypassed the product lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readerDone:
		if err == nil || !strings.Contains(err.Error(), "奖品规格不存在") {
			t.Fatalf("save accepted deleted SKU or failed for the wrong reason: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if n := d.Client.LotteryPrize.Query().Where(lotteryprize.Mode("card")).CountX(ctx); n != 0 {
		t.Fatal("failed save left an orphan SKU prize", n)
	}
	if got := d.Client.LotteryActivity.GetX(ctx, activity.Id); got.Revision != activity.Revision {
		t.Fatal("failed save changed the activity revision")
	}
}
