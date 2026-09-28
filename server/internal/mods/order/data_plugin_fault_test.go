package order

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	invport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func TestP2FaultsAndUnrestrictedProduct(t *testing.T) {
	for _, fault := range []string{"init-throw", "init-loop", "timeout", "invalid-output", "disabled", "uninstalled", "missing-package", "disabled-manual", "disabled-referral", "disabled-auto", "core-unavailable"} {
		t.Run(fault, func(t *testing.T) {
			e := p2Fixture(t, "sqlite")
			reason := "PLUGIN_UNAVAILABLE"
			switch fault {
			case "init-throw", "init-loop":
				script := `throw new Error("private")`
				if fault == "init-loop" {
					script = `while(true){}`
				}
				if _, err := e.importScript(t, "0.1.1", script, "upgrade", 2); err == nil {
					t.Fatal("broken initialization activated")
				}
				state, err := e.manager.Status(e.ctx, e.command.PluginID)
				if err != nil || state.State.DesiredGeneration != 2 || state.ObservedDigest != e.command.TargetDigest {
					t.Fatalf("old runtime replaced: %+v %v", state, err)
				}
				if _, err = e.uc.CreateOrder(e.ctx, e.input(e.allowed.ID)); err != nil {
					t.Fatal("failed prepare broke old runtime", err)
				}
				return
			case "timeout":
				e.install(t, "0.1.1", `function evaluate(i){while(true){}}`, "upgrade", 2)
			case "invalid-output":
				e.install(t, "0.1.1", `function evaluate(i){return {allow:true,reason:'MEMBER_LEVEL_DENIED'}}`, "upgrade", 2)
			case "disabled", "uninstalled":
				c := e.command
				c.OperationID = p2UUID()
				c.TargetDigest = ""
				c.ExpectedGeneration = 2
				c.Action = "disable"
				if fault == "uninstalled" {
					c.Action = "uninstall"
				}
				if _, err := e.manager.Operate(e.ctx, c); err != nil {
					t.Fatal(err)
				}
			case "missing-package":
				if err := os.Remove(filepath.Join(e.packageRoot, e.command.PluginID, e.command.TargetDigest, "archive.zip")); err != nil {
					t.Fatal(err)
				}
				repo := plugin.NewRepo(e.d, plugin.NewCoordinator(), nil)
				m := plugin.NewManager(repo, e.store, plugin.NewRuntimeLoader(e.store))
				if err := m.Reconcile(e.ctx); err != nil {
					t.Fatal("one bad package prevented startup", err)
				}
				e.uc.PluginGate = plugin.NewRequiredGate(repo)
			case "disabled-manual":
				e.d.Client.MemberLevel.UpdateOneID(e.level.ID).SetEnabled(false).ExecX(e.ctx)
			case "disabled-referral", "disabled-auto":
				e.d.Client.MemberLevel.UpdateOneID(e.auto.ID).SetEnabled(false).ExecX(e.ctx)
			case "core-unavailable":
				e.d.Client.Product.UpdateOneID(e.product.ID).SetStatus(0).ExecX(e.ctx)
				reason = "商品"
			}
			if fault == "disabled-auto" {
				reason = "MEMBER_LEVEL_DENIED"
			}
			in := e.input(e.allowed.ID)
			if fault == "disabled-auto" {
				in = e.input(e.automatic.ID)
			}
			if fault == "disabled-referral" {
				in = e.input(e.referral.ID)
			}
			before := p2Snapshot(t, e.d)
			if _, err := e.uc.CreateOrder(e.ctx, in); err == nil || (!strings.Contains(err.Error(), reason) && fault != "core-unavailable") {
				t.Fatalf("fault not denied: %v", err)
			}
			if before != p2Snapshot(t, e.d) {
				t.Fatal("fault changed business data")
			}
			// An unrelated product does not require this runtime. Use a no-level buyer
			// so the intentionally corrupted assigned level is irrelevant.
			p := e.d.Client.Product.Create().SetName("ordinary").SetSlug("ordinary").SetPrice(100).SetStatus(1).SaveX(e.ctx)
			uc := *e.uc
			uc.Inv = fakeInventory{}
			ordinary := e.input(e.plain.ID)
			ordinary.Items = []OrderItemInput{{ProductID: p.ID, Quantity: 1}}
			if _, err := uc.CreateOrder(e.ctx, ordinary); err != nil {
				t.Fatal("fault affected unrelated product", err)
			}
		})
	}
}
func TestP2StorefrontHTTPAndCart(t *testing.T) {
	e := p2Fixture(t, "sqlite")
	ordinary := e.d.Client.Product.Create().SetName("cart-first").SetSlug("cart-first").SetPrice(100).SetStatus(1).SaveX(e.ctx)
	service := NewStoreOrderService(e.uc, nil)
	var actor uint64
	server := khttp.NewServer(khttp.Timeout(15*time.Second), khttp.Filter(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := e.ctx
			if actor > 0 {
				ctx = identity.WithClaims(ctx, &authn.Claims{Subject: actor, Realm: authn.RealmUser})
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}))
	storefrontv1.RegisterStoreOrderServiceHTTPServer(server, service)
	for _, tc := range []struct {
		name   string
		user   uint64
		cart   bool
		status int
		reason string
	}{{"guest", 0, false, 401, "LOGIN_REQUIRED"}, {"wrong", e.denied.ID, false, 403, "MEMBER_LEVEL_DENIED"}, {"cart", e.denied.ID, true, 403, "MEMBER_LEVEL_DENIED"}, {"ordinary-buyer", e.allowed.ID, false, 200, "order_no"}} {
		t.Run(tc.name, func(t *testing.T) {
			actor = tc.user
			items := fmt.Sprintf(`{"product_id":%s,"quantity":1}`, fmt.Sprint(e.product.ID))
			if tc.cart {
				items = fmt.Sprintf(`{"product_id":%s,"quantity":1},`, fmt.Sprint(ordinary.ID)) + items
			}
			body := `{"items":[` + items + `],"query_password":"test-password","guest_contact":"p2@example.test","contact":"p2@example.test"}`
			before := p2Snapshot(t, e.d)
			req := httptest.NewRequest("POST", "/api/v1/storefront/orders", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.reason) {
				t.Fatalf("HTTP gate: %d %s", w.Code, w.Body.String())
			}
			if tc.status != 200 && before != p2Snapshot(t, e.d) {
				t.Fatal("HTTP/cart denial changed data")
			}
		})
	}
	// A direct call with no IP also traverses the required gate (no transport bypass).
	if _, err := e.uc.CreateOrder(context.Background(), e.input(e.denied.ID)); err == nil {
		t.Fatal("direct call bypassed gate")
	}
}

type reservationProbe struct {
	invport.Inventory
	calls int
}

func (r *reservationProbe) Reserve(ctx context.Context, site uint64, items []invport.ReserveItem) (*invport.Reservation, error) {
	r.calls++
	return r.Inventory.Reserve(ctx, site, items)
}
func TestP2DenialPrecedesReservationAndCharges(t *testing.T) {
	e := p2Fixture(t, "sqlite")
	probe := &reservationProbe{Inventory: e.uc.Inv}
	e.uc.Inv = probe
	w := wallet.NewWalletRepoImpl(e.d)
	e.uc.Points = pointsDebitAdapter{repo: w}
	e.uc.Coupon = coupon.NewCouponRepoImpl(e.d)
	if err := w.PointCreditInTx(e.ctx, wallet.PointEntry{UserID: e.denied.ID, Direction: "in", Type: "adjust", Amount: 1000, Reference: "p2-points"}); err != nil {
		t.Fatal(err)
	}
	e.d.Client.Coupon.Create().SetName("untouched").SetCode("p2-untouched").SetType("fixed").SetValue(50).SaveX(e.ctx)
	e.d.Client.Product.UpdateOneID(e.product.ID).SetPointsRequired(100).ExecX(e.ctx)
	for _, points := range []bool{false, true} {
		in := e.input(e.denied.ID)
		in.UsePoints = points
		in.CouponCode = "p2-untouched"
		before := p2Snapshot(t, e.d)
		if _, err := e.uc.CreateOrder(e.ctx, in); err == nil || !strings.Contains(err.Error(), "MEMBER_LEVEL_DENIED") {
			t.Fatal("gate did not precede business dependencies", err)
		}
		if probe.calls != 0 {
			t.Fatal("inventory called before required gate; rollback is insufficient")
		}
		if before != p2Snapshot(t, e.d) {
			t.Fatal("denial modified seeded points/coupon/inventory")
		}
	}
}
