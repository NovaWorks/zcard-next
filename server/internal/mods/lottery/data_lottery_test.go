package lottery

import (
	"context"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storev1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotterydraw"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	_ "modernc.org/sqlite"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Repo, *AdminService, context.Context, uint64) {
	t.Helper()
	handle, e := db.SQLite.Open(fmt.Sprintf("file:lottery-%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if e != nil {
		t.Fatal(e)
	}
	handle.SetMaxOpenConns(1)
	c := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if e = c.Schema.Create(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	cipher, _ := inventory.NewCardCipher(make([]byte, 32))
	r := NewRepo(&data.Data{Client: c, DB: handle, Dialect: db.SQLite}, cipher)
	r.now = func() time.Time { return time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC) }
	r.random = func() (int, error) { return 0, nil }
	ctx := context.Background()
	u := c.User.Create().SetUsername("winner").SetPasswordHash("hash").SaveX(ctx)
	ctx = identity.WithClaims(ctx, &authn.Claims{Subject: u.ID})
	return r, NewAdminService(r), ctx, u.ID
}
func config(r *Repo, mode string) *adminv1.LotteryActivity {
	return &adminv1.LotteryActivity{Name: "免费抽奖", StartAt: r.now().Add(-time.Hour).Unix(), EndAt: r.now().AddDate(0, 0, 7).Unix(), Timezone: "Asia/Shanghai", ChanceMode: "once", ChanceCount: 3, Prizes: []*adminv1.LotteryPrize{{Name: "奖品", Mode: mode, Probability: 10000, Quantity: 20, Content: "领取说明或私密链接"}}}
}
func publish(t *testing.T, s *AdminService, ctx context.Context, v *adminv1.LotteryActivity) *adminv1.LotteryActivity {
	t.Helper()
	a, e := s.SaveLotteryActivity(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	a, e = s.SetLotteryStatus(ctx, &adminv1.LotteryStatusRequest{Id: a.Id, Status: "live", Revision: a.Revision})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestCardDrawAtomicRetryAndInventoryGuards(t *testing.T) {
	r, s, ctx, uid := fixture(t)
	c := r.Data.Client
	p := c.Product.Create().SetName("奖品商品").SetSlug("prize").SetStatus(1).SetFactoryPrice(123).SaveX(ctx)
	for i := 0; i < 4; i++ {
		sealed, _ := r.Cipher.Seal(fmt.Sprint("secret", i), p.ID, 0)
		c.Card.Create().SetProductID(p.ID).SetContent(sealed).SetContentHash(fmt.Sprint(i)).SaveX(ctx)
	}
	v := config(r, "card")
	v.Prizes[0].ProductId = p.ID
	a := publish(t, s, ctx, v)
	d, e := r.Draw(ctx, a.Id, uid, "same-request-123456")
	if e != nil {
		t.Fatal(e)
	}
	if d.Status != "delivered" || d.CardID == nil || d.Cost != 123 {
		t.Fatalf("bad result %v", d)
	}
	plain, e := r.Content(ctx, d)
	if e != nil || !strings.HasPrefix(plain, "secret") {
		t.Fatal("missing receipt", e)
	}
	if strings.Contains(string(d.Content), "secret") {
		t.Fatal("plaintext at rest")
	}
	r.now = func() time.Time { return time.Unix(a.EndAt+100, 0) }
	again, e := r.Draw(ctx, a.Id, uid, "same-request-123456")
	if e != nil || again.ID != d.ID {
		t.Fatal("committed retry after end lost", e)
	}
	if c.LotteryDraw.Query().CountX(ctx) != 1 || c.Card.Query().Where(card.StatusEQ(card.StatusUsed)).CountX(ctx) != 1 {
		t.Fatal("duplicate award")
	}
	if c.Order.Query().CountX(ctx) != 0 || c.Payment.Query().CountX(ctx) != 0 || c.WalletTransaction.Query().CountX(ctx) != 0 || c.AffiliateCommission.Query().CountX(ctx) != 0 {
		t.Fatal("lottery changed order or finances")
	}
	inv := inventory.NewAdminInventoryService(inventory.NewCardRepoImpl(r.Data, r.Cipher), r.Data)
	if _, e = inv.ToggleCard(ctx, &adminv1.ToggleCardRequest{Id: *d.CardID, Enable: true}); e == nil {
		t.Fatal("awarded card revived")
	}
	if _, e = inventory.NewCardRepoImpl(r.Data, r.Cipher).ReleaseExpired(ctx, 0); e != nil {
		t.Fatal(e)
	}
	if c.Card.GetX(ctx, *d.CardID).Status != card.StatusUsed {
		t.Fatal("TTL released award")
	}
	store := NewStoreService(r)
	if _, e = store.GetMyLotteryDraw(context.Background(), &storev1.LotteryResultRequest{DrawNo: d.DrawNo}); e == nil {
		t.Fatal("guest read award")
	}
	other := identity.WithClaims(ctx, &authn.Claims{Subject: uid + 999})
	if _, e = store.GetMyLotteryDraw(other, &storev1.LotteryResultRequest{DrawNo: d.DrawNo}); e == nil {
		t.Fatal("other user read award")
	}
	if _, e = r.Result(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9}), d.DrawNo, uid); e == nil {
		t.Fatal("cross-site award exposed")
	}
	if _, e = r.Cipher.OpenLottery(d.Content, "draw:"+d.DrawNo, 9); e == nil {
		t.Fatal("receipt AAD ignored tenant")
	}
}
func TestDailyGrantsAndManualFulfillment(t *testing.T) {
	r, s, ctx, uid := fixture(t)
	v := config(r, "manual")
	v.ChanceMode = "daily"
	a := publish(t, s, ctx, v)
	for i := 0; i < 3; i++ {
		n, _, e := r.Claim(ctx, a.Id, uid)
		if e != nil || n != 3 {
			t.Fatal("repeated grant", n, e)
		}
	}
	d, e := r.Draw(ctx, a.Id, uid, "daily-request-123456")
	if e != nil || d.Status != "pending" {
		t.Fatal(e)
	}
	if e = r.Deliver(ctx, d.DrawNo, "已线下发放"); e != nil {
		t.Fatal(e)
	}
	if e = r.Deliver(ctx, d.DrawNo, "重复点击"); e != nil {
		t.Fatal(e)
	}
	got, _ := r.Result(ctx, d.DrawNo, uid)
	if got.Remark != "已线下发放" || got.Status != "delivered" {
		t.Fatal("duplicate delivery overwrote audit")
	}
	if e = r.Grant(ctx, a.Id, uid, 2, "补发", "manual-grant-123456"); e != nil {
		t.Fatal(e)
	}
	if e = r.Grant(ctx, a.Id, uid, 2, "补发", "manual-grant-123456"); e != nil {
		t.Fatal(e)
	}
	n, _, _ := r.Claim(ctx, a.Id, uid)
	if n != 4 {
		t.Fatal("manual grant duplicated", n)
	}
	r.now = func() time.Time { return time.Date(2026, 9, 13, 16, 0, 0, 0, time.UTC) }
	n, per, e := r.Claim(ctx, a.Id, uid)
	if e != nil || n != 3 || per != "2026-09-14" {
		t.Fatal("timezone daily reset", n, per, e)
	}
	paused, e := s.SetLotteryStatus(ctx, &adminv1.LotteryStatusRequest{Id: a.Id, Status: "paused", Revision: a.Revision})
	if e != nil {
		t.Fatal(e)
	}
	paused.ChanceCount = 5
	if _, e = s.SaveLotteryActivity(ctx, paused); e == nil {
		t.Fatal("published quota changed")
	}
}
func TestConcurrentSameRequestAndQuota(t *testing.T) {
	r, s, ctx, uid := fixture(t)
	a := publish(t, s, ctx, config(r, "text"))
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := r.Draw(ctx, a.Id, uid, "concurrent-123456"); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if r.Data.Client.LotteryDraw.Query().CountX(ctx) != 1 {
		t.Fatal("duplicate result")
	}
	n, _, _ := r.Claim(ctx, a.Id, uid)
	if n != 2 {
		t.Fatal("duplicate charge", n)
	}
	for i := 0; i < 2; i++ {
		if _, e := r.Draw(ctx, a.Id, uid, fmt.Sprintf("next-request-%06d", i)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := r.Draw(ctx, a.Id, uid, "next-request-999999"); e == nil {
		t.Fatal("negative chances")
	}
}
func TestUnavailablePrizesDoNotChangeOddsOrConsumeChance(t *testing.T) {
	r, s, ctx, uid := fixture(t)
	v := config(r, "text")
	v.Prizes[0].Quantity = 1
	a := publish(t, s, ctx, v)
	if _, e := r.Draw(ctx, a.Id, uid, "first-request-1234"); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Draw(ctx, a.Id, uid, "empty-request-1234"); e == nil {
		t.Fatal("depleted prize accepted")
	}
	n, _, _ := r.Claim(ctx, a.Id, uid)
	if n != 2 {
		t.Fatal("failure consumed chance")
	}
	if r.Data.Client.LotteryDraw.Query().CountX(ctx) != 1 {
		t.Fatal("failed draw replaced with missed result")
	}
}
func TestProbabilityBoundariesAndSecretSnapshots(t *testing.T) {
	r, s, ctx, uid := fixture(t)
	v := config(r, "text")
	v.Prizes[0].Probability = 2500
	a := publish(t, s, ctx, v)
	r.random = func() (int, error) { return 2499, nil }
	d, e := r.Draw(ctx, a.Id, uid, "boundary-win-12345")
	if e != nil || d.Status != "delivered" {
		t.Fatal(e)
	}
	r.random = func() (int, error) { return 2500, nil }
	d2, e := r.Draw(ctx, a.Id, uid, "boundary-miss-1234")
	if e != nil || d2.Status != "missed" || len(d2.Content) > 0 {
		t.Fatal("boundary wrong", e)
	}
	raw := fmt.Sprint(d.RuleSnapshot)
	if strings.Contains(raw, "私密链接") {
		t.Fatal("snapshot leaked content")
	}
	paused, e := s.SetLotteryStatus(ctx, &adminv1.LotteryStatusRequest{Id: a.Id, Status: "paused", Revision: a.Revision})
	if e != nil {
		t.Fatal(e)
	}
	paused.Prizes[0].Content = "新内容"
	if _, e = s.SaveLotteryActivity(ctx, paused); e != nil {
		t.Fatal(e)
	}
	content, _ := r.Content(ctx, d)
	if content != "领取说明或私密链接" {
		t.Fatal("old result changed")
	}
	v.Prizes[0].Probability = 10001
	if _, e = s.SaveLotteryActivity(ctx, v); e == nil {
		t.Fatal("invalid odds accepted")
	}
	if r.Data.Client.LotteryDraw.Query().Where(lotterydraw.Status("missed")).CountX(ctx) != 1 {
		t.Fatal("missed state")
	}
}

func TestHTTPPrivacyAndFailedIssueRollback(t *testing.T) {
	r, s, ctx, uid := fixture(t)
	a := publish(t, s, ctx, config(r, "text"))
	store := NewStoreService(r)
	srv := kratoshttp.NewServer()
	storev1.RegisterStoreLotteryServiceHTTPServer(srv, store)
	request := func(path string, withUser bool) *httptest.ResponseRecorder {
		q := httptest.NewRequest("GET", path, nil)
		if withUser {
			q = q.WithContext(ctx)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, q)
		return w
	}
	w := request(fmt.Sprintf("/api/v1/storefront/lottery/activities/%d", a.Id), false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "领取说明或私密链接") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("public privacy: %d %s", w.Code, w.Body.String())
	}
	d, e := r.Draw(ctx, a.Id, uid, "http-private-123456")
	if e != nil {
		t.Fatal(e)
	}
	w = request("/api/v1/storefront/lottery/draws/"+d.DrawNo, false)
	if w.Code != 401 {
		t.Fatal("guest result", w.Code)
	}
	w = request("/api/v1/storefront/lottery/draws/"+d.DrawNo, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "领取说明或私密链接") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("owner result", w.Code, w.Body.String())
	}
	other := r.Data.Client.User.Create().SetUsername("other-real-user").SetPasswordHash("hash").SaveX(ctx)
	if _, e = store.GetMyLotteryDraw(identity.WithClaims(ctx, &authn.Claims{Subject: other.ID}), &storev1.LotteryResultRequest{DrawNo: d.DrawNo}); e == nil {
		t.Fatal("other member read receipt")
	}
	// Decryption failure must roll back prize quota and chance, never create a false win.
	p := r.Data.Client.Product.Create().SetName("bad-card").SetSlug("bad-card").SetStatus(1).SaveX(ctx)
	r.Data.Client.Card.Create().SetProductID(p.ID).SetContent([]byte("corrupt")).SetContentHash("corrupt").SaveX(ctx)
	cfg := config(r, "card")
	cfg.Prizes[0].ProductId = p.ID
	a = publish(t, s, ctx, cfg)
	n, _, e := r.Claim(ctx, a.Id, uid)
	if e != nil || n != 3 {
		t.Fatal(e)
	}
	if _, e = r.Draw(ctx, a.Id, uid, "decrypt-fail-12345"); e == nil {
		t.Fatal("corrupt card issued")
	}
	n, _, e = r.Claim(ctx, a.Id, uid)
	if e != nil || n != 3 {
		t.Fatal("failed issue lost chance", n, e)
	}
	if r.Data.Client.LotteryDraw.Query().Where(lotterydraw.ActivityID(a.Id)).CountX(ctx) != 0 {
		t.Fatal("failed draw persisted")
	}
	if r.Data.Client.Card.Query().Where(card.ProductID(p.ID), card.StatusEQ(card.StatusAvailable)).CountX(ctx) != 1 {
		t.Fatal("failed card consumed")
	}
}
func TestCatalogGuardsAndLiveEligibility(t *testing.T) {
	r, s, ctx, uid := fixture(t)
	c := r.Data.Client
	p := c.Product.Create().SetName("prize-sku").SetSlug("prize-sku").SetStatus(1).SaveX(ctx)
	sk := c.ProductSku.Create().SetProductID(p.ID).SetName("规格一").SetSpecValues(map[string]string{"规格": "一"}).SaveX(ctx)
	sealed, _ := r.Cipher.Seal("sku-card", p.ID, 0)
	c.Card.Create().SetProductID(p.ID).SetSkuID(sk.ID).SetContent(sealed).SetContentHash("sku-card").SaveX(ctx)
	cfg := config(r, "card")
	cfg.Prizes[0].ProductId = p.ID
	if _, e := s.SaveLotteryActivity(ctx, cfg); e == nil {
		t.Fatal("missing explicit sku accepted")
	}
	cfg.Prizes[0].SkuId = sk.ID
	a := publish(t, s, ctx, cfg)
	cat := catalog.NewProductRepoImpl(r.Data, nil)
	if e := cat.DeleteSku(ctx, sk.ID); e == nil {
		t.Fatal("active prize sku deleted")
	}
	c.Product.UpdateOneID(p.ID).SetStatus(0).ExecX(ctx)
	if _, e := r.Draw(ctx, a.Id, uid, "off-shelf-1234567"); e == nil {
		t.Fatal("off-shelf product awarded")
	}
	c.Product.UpdateOneID(p.ID).SetStatus(1).ExecX(ctx)
	d, e := r.Draw(ctx, a.Id, uid, "sku-issue-1234567")
	if e != nil || d.SkuID != sk.ID {
		t.Fatal("sku delivery", e)
	}
	inv := inventory.NewAdminInventoryService(inventory.NewCardRepoImpl(r.Data, r.Cipher), r.Data)
	rows, e := inv.ListCards(ctx, &adminv1.ListCardsRequest{ProductId: p.ID})
	if e != nil || len(rows.Cards) != 1 || rows.Cards[0].LotteryDrawNo != d.DrawNo {
		t.Fatal("award inventory label", rows, e)
	}
	c.Product.UpdateOneID(p.ID).SetStatus(0).ExecX(ctx)
	svc := catalog.NewAdminCatalogService(cat, nil, nil, nil)
	pre, e := svc.PreviewDeleteProduct(ctx, &adminv1.GetProductRequest{Id: p.ID})
	if e != nil || pre.DeleteBlockReason == "" || pre.DeleteOrdersBlockReason == "" {
		t.Fatal("lottery history purge guard", pre, e)
	}
}

func TestEndedActivityCannotResetThroughPauseOrEdit(t *testing.T) {
	r, s, ctx, _ := fixture(t)
	a := publish(t, s, ctx, config(r, "text"))
	a, e := s.SetLotteryStatus(ctx, &adminv1.LotteryStatusRequest{Id: a.Id, Revision: a.Revision, Status: "ended"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetLotteryStatus(ctx, &adminv1.LotteryStatusRequest{Id: a.Id, Revision: a.Revision, Status: "paused"}); e == nil {
		t.Fatal("ended reset through pause")
	}
	b := publish(t, s, ctx, config(r, "text"))
	b, e = s.SetLotteryStatus(ctx, &adminv1.LotteryStatusRequest{Id: b.Id, Revision: b.Revision, Status: "paused"})
	if e != nil {
		t.Fatal(e)
	}
	r.now = func() time.Time { return time.Unix(b.EndAt+1, 0) }
	b.EndAt += 86400
	if _, e = s.SaveLotteryActivity(ctx, b); e == nil {
		t.Fatal("expired paused activity extended")
	}
}
