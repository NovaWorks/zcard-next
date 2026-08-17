//go:build integration

// T2-1 真并发防超卖（P0-05，M1 验收条款「并发防超卖 100/10——MySQL + SQLite
// 双路径」的 MySQL/PG 兑付）。
//
// 与 SQLite 顺序版（inventory/inventory_test.go TestSequentialReserve_10of100）
// 的本质区别：100 goroutine × 独立连接池，走 FOR UPDATE SKIP LOCKED 真锁竞争
// 与真隔离级别——SQLite 单写者串行路径覆盖不到的 deadlock/锁等待/CAS 竞争面。
package testint

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
)

const (
	oversellCards  = 10  // 库存
	oversellBuyers = 100 // 并发买单
)

// TestOversellMySQL / TestOversellPG 同一断言、两个方言（Open 差异在骨架内）。
func TestOversellMySQL(t *testing.T) { runOversell(MySQL(t)) }
func TestOversellPG(t *testing.T)    { runOversell(PG(t)) }

func runOversell(h *Harness) {
	t := h.T
	ctx := context.Background()
	productID := seedOversellCards(h)

	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}

	var success, fail int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, oversellBuyers)
	for i := 0; i < oversellBuyers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // 同时起跑，最大化锁竞争窗口
			// 共享池（50 连接上限，生产拓扑）：每 goroutine 独立事务，
			// 50 路并发 FOR UPDATE SKIP LOCKED 真锁竞争
			repo := inventory.NewCardRepoImpl(h.Data, cipher)
			err := data.Tx(ctx, h.Data, func(txCtx context.Context) error {
				_, err := repo.Reserve(txCtx, 0, []port.ReserveItem{
					{ProductID: productID, Quantity: 1},
				})
				return err
			})
			if err == nil {
				atomic.AddInt64(&success, 1)
			} else {
				atomic.AddInt64(&fail, 1)
				if !isInsufficient(err) {
					errs[idx] = fmt.Errorf("失败原因非库存不足（%w）", err)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("goroutine %d 异常: %v", i, e)
		}
	}
	if success != oversellCards {
		t.Fatalf("防超卖失败：成功 %d 单（期望恰好 %d），失败 %d", success, oversellCards, fail)
	}
	if fail != oversellBuyers-oversellCards {
		t.Fatalf("失败计数异常：%d（期望 %d）", fail, oversellBuyers-oversellCards)
	}
	// 终态一致：0 available / 10 reserved，且无重复占用
	avail, err := h.Data.Client.Card.Query().
		Where(card.ProductID(productID), card.StatusEQ(card.StatusAvailable)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reservedRows, err := h.Data.Client.Card.Query().
		Where(card.ProductID(productID), card.StatusEQ(card.StatusReserved)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if avail != 0 || len(reservedRows) != oversellCards {
		t.Fatalf("终态不一致：available=%d reserved=%d（期望 0/%d）", avail, len(reservedRows), oversellCards)
	}
	seen := map[uint64]bool{}
	for _, c := range reservedRows {
		if seen[c.ID] {
			t.Fatalf("双卖：卡 %d 被重复占用", c.ID)
		}
		seen[c.ID] = true
	}
	t.Logf("防超卖双路径通过：%d 并发买 %d 卡 → 恰好 %d 成功 / %d 失败 / 0 双卖（%s）",
		oversellBuyers, oversellCards, success, fail, h.D)
}

func isInsufficient(err error) bool {
	for e := err; e != nil; {
		if e == inventory.ErrInsufficient { //nolint:errorlint // sentinel 比较
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// seedOversellCards 建商品 + oversellCards 张可用卡（Reserve 不触碰密文，
// Content 直写密文字节即可）。
func seedOversellCards(h *Harness) uint64 {
	t := h.T
	ctx := context.Background()
	p, err := h.Data.Client.Product.Create().
		SetSubsiteID(0).
		SetName("防超卖集成测试").
		SetSlug("it-oversell-" + h.target).
		SetPrice(1000).
		SetStockType(product.StockTypeCard).
		Save(ctx)
	if err != nil {
		t.Fatalf("建商品失败: %v", err)
	}
	for i := 0; i < oversellCards; i++ {
		_, err := h.Data.Client.Card.Create().
			SetProductID(p.ID).
			SetSubsiteID(0).
			SetContent([]byte{byte(i)}).
			SetContentHash(fmt.Sprintf("hash-it-%d", i)).
			SetStatus(card.StatusAvailable).
			Save(ctx)
		if err != nil {
			t.Fatalf("建卡 %d 失败: %v", i, err)
		}
	}
	return p.ID
}
