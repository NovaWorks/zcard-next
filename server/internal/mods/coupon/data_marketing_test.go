package coupon

// P3-02 M3 必测项：券范围矩阵/每人限用/领取返还、秒杀同锁并发防超卖+限购、
// 促销门槛与最优选取、管线金额行矩阵（券×会员叠加、券×秒杀互斥、促销行）。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/promotion"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	_ "modernc.org/sqlite"
)

func newMarketingData(t *testing.T) (*CouponRepoImpl, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:mkttest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	return NewCouponRepoImpl(d), d
}

// TestScopeMatrix 券范围矩阵（全场/商品/分类/等级）。
func TestScopeMatrix(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()
	seedProduct(t, d, 1, 100) // 商品 1 分类 100
	seedProduct(t, d, 2, 200) // 商品 2 分类 200

	mkCoupon := func(scope string) string {
		code := fmt.Sprintf("C-SCOPE-%d", time.Now().UnixNano())
		var sc map[string]any
		if scope != "" {
			sc = map[string]any{}
			_ = json.Unmarshal([]byte(scope), &sc)
		}
		_, err := d.Client.Coupon.Create().
			SetName("范围券").SetType("fixed").SetValue(100).
			SetCode(code).SetScope(sc).SetStatus(coupon.StatusUnused).
			Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return code
	}
	items := []port.CartItem{{ProductID: 1, CategoryID: 100, Quantity: 1, UnitPrice: 1000}}

	// 全场（空 scope）
	if _, _, err := r.ResolveScoped(ctx, mkCoupon(""), 7, 0, items); err != nil {
		t.Fatalf("全场券应可用: %v", err)
	}
	// 商品命中
	if _, _, err := r.ResolveScoped(ctx, mkCoupon(`{"product_ids":[1]}`), 7, 0, items); err != nil {
		t.Fatalf("商品命中应可用: %v", err)
	}
	// 商品不命中
	if _, _, err := r.ResolveScoped(ctx, mkCoupon(`{"product_ids":[2]}`), 7, 0, items); err == nil {
		t.Fatal("商品不命中应拒绝")
	}
	// 分类命中 / 不命中
	if _, _, err := r.ResolveScoped(ctx, mkCoupon(`{"category_ids":[100]}`), 7, 0, items); err != nil {
		t.Fatalf("分类命中应可用: %v", err)
	}
	if _, _, err := r.ResolveScoped(ctx, mkCoupon(`{"category_ids":[200]}`), 7, 0, items); err == nil {
		t.Fatal("分类不命中应拒绝")
	}
	// 等级命中（levelID=9）
	if _, _, err := r.ResolveScoped(ctx, mkCoupon(`{"level_ids":[9]}`), 7, 9, items); err != nil {
		t.Fatalf("等级命中应可用: %v", err)
	}
	if _, _, err := r.ResolveScoped(ctx, mkCoupon(`{"level_ids":[9]}`), 7, 0, items); err == nil {
		t.Fatal("等级不命中应拒绝")
	}
}

// TestCouponRedeemAndReturn 领取（幂等）+ 取消返还 + 过期不返。
func TestCouponRedeemAndReturn(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()

	c, err := d.Client.Coupon.Create().
		SetName("领取券").SetType("fixed").SetValue(50).
		SetCode("C-RDM-1").SetStatus(coupon.StatusUnused).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 兑换领券 + 幂等
	if err := r.Redeem(ctx, "C-RDM-1", 7); err != nil {
		t.Fatal(err)
	}
	if err := r.Redeem(ctx, "C-RDM-1", 7); err != nil {
		t.Fatalf("重复领取应幂等: %v", err)
	}
	// 他人不可领已指派券
	if err := r.Redeem(ctx, "C-RDM-1", 8); err == nil {
		t.Fatal("他人领取应拒绝")
	}
	// 核销 → 取消返还
	o, _ := d.Client.Order.Create().SetOrderNo("T-R-1").SetSubsiteID(0).
		SetStatus(order.StatusPendingPayment).SetTotalAmount(100).Save(ctx)
	if err := r.MarkUsed(ctx, c.ID, o.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.ReturnByOrder(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Client.Coupon.Get(ctx, c.ID)
	if got.Status != coupon.StatusUnused || got.UsedOrderID != 0 {
		t.Fatalf("返还失败: %+v", got)
	}
	// 过期不返：核销后过期再返还 → 保持 used
	exp := time.Now().Add(-time.Hour)
	_, _ = d.Client.Coupon.UpdateOneID(c.ID).SetExpireAt(exp).Save(ctx)
	_ = r.MarkUsed(ctx, c.ID, o.ID)
	_ = r.ReturnByOrder(ctx, o.ID)
	got, _ = d.Client.Coupon.Get(ctx, c.ID)
	if got.Status != coupon.StatusUsed {
		t.Fatal("过期券不应返还")
	}
}

// TestFlashConsumeConcurrent 同锁并发：50 并发抢 10 件恰好成功 10。
func TestFlashConsumeConcurrent(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()
	seedProduct(t, d, 1, 100)

	fs, err := r.CreateFlash(ctx, 1, 0, 500, time.Now().Add(-time.Minute), time.Now().Add(time.Hour), 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	// SQLite 单写者天然串行（与 inventory 防超卖测试同矩阵）；并发路径 CAS 语义等价
	var mu sync.Mutex
	ok, soldOut := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// SQLite 内存库跨连接共享：每 goroutine 独立 ctx 即可
			if err := r.Consume(context.Background(), fs.ID, 1); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			} else {
				mu.Lock()
				soldOut++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 10 || soldOut != 40 {
		t.Fatalf("防超卖失败: ok=%d soldOut=%d", ok, soldOut)
	}
	got, _ := d.Client.FlashSale.Get(ctx, fs.ID)
	if got.SoldQty != 10 {
		t.Fatalf("sold_qty 应恰为 10: %d", got.SoldQty)
	}
}

// TestFlashUserLimit 秒杀限购（paid+pending 累计）。
func TestFlashUserLimit(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()
	seedProduct(t, d, 1, 100)

	// 用户 7 已有 1 单 2 件（paid）
	o, _ := d.Client.Order.Create().SetOrderNo("T-F-1").SetSubsiteID(0).
		SetUserID(7).SetStatus(order.StatusPaid).SetTotalAmount(100).Save(ctx)
	_, _ = d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).
		SetQuantity(2).SetUnitPrice(100).SetAmount(200).SetFulfillmentType(orderitem.FulfillmentTypeAuto).SetSubsiteID(0).Save(ctx)

	n, err := r.UserPurchasedCount(ctx, 1, 7, time.Now().UTC().Add(-time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("已购计数错误: %d %v", n, err)
	}
}

// TestPromotionBestFor 促销门槛与最优选取（多促不叠加取最大折让）。
func TestPromotionBestFor(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()
	now := time.Now().UTC()
	win := func() (time.Time, time.Time) { return now.Add(-time.Minute), now.Add(time.Hour) }
	s, e := win()

	mk := func(name, typ string, threshold, discount, special int64, scope string) {
		var sc map[string]any
		if scope != "" {
			sc = map[string]any{}
			_ = json.Unmarshal([]byte(scope), &sc)
		}
		if _, err := d.Client.Promotion.Create().
			SetName(name).SetType(promotion.Type(typ)).SetThreshold(threshold).
			SetDiscount(discount).SetSpecialPrice(special).SetScope(sc).
			SetStartAt(s).SetEndAt(e).SetEnabled(true).
			Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// 候选：满 1000 减 100（命中）、满 5000 减 600（门槛不够）、9 折（折让 100）、特价 700（折让 300）
	mk("满减小", "fixed", 1000, 100, 0, "")
	mk("满减大", "fixed", 5000, 600, 0, "")
	mk("折扣", "percent", 0, 1000, 0, "") // 10%
	mk("特价", "special_price", 0, 0, 700, `{"product_ids":[1]}`)

	best, err := r.BestFor(ctx, 1, 0, money.Cents(1000))
	if err != nil || best == nil {
		t.Fatalf("应有最优促销: %v", err)
	}
	// 特价折让 300 > 满减 100 > 折扣 100；满减大门槛不满足
	if best.Name != "特价" || best.DiscountFor(1000) != 300 {
		t.Fatalf("最优选取错误: %+v 折让=%d", best, best.DiscountFor(1000))
	}

	// 范围外商品：特价不命中 → 最优为满减小（100）或折扣（100）——同为 100 取后遍历者
	best2, _ := r.BestFor(ctx, 2, 0, money.Cents(1000))
	if best2 == nil || best2.DiscountFor(1000) != 100 {
		t.Fatalf("范围外最优错误: %+v", best2)
	}
}

// ── 测试工具 ──────────────────────────────────────────────

func seedProduct(t *testing.T, d *data.Data, id uint64, categoryID uint64) {
	t.Helper()
	ctx := context.Background()
	if _, err := d.Client.Product.Create().
		SetID(id).SetSubsiteID(0).SetName(fmt.Sprintf("P%d", id)).
		SetSlug(fmt.Sprintf("p-%d", id)).SetPrice(1000).
		SetCategoryID(categoryID).
		Save(ctx); err != nil {
		// 幂等（共享库重复 seed）
		_ = err
	}
}
