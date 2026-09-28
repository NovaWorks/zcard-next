package order

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	orderent "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/plugindata"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	memberport "github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin"
	pluginport "github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func p2UUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	v := hex.EncodeToString(b[:])
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
}
func p2Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type p2Env struct {
	d                                           *data.Data
	uc                                          *OrderUsecase
	repo                                        *plugin.Repo
	manager                                     *plugin.Manager
	store                                       *plugin.FilePackages
	key                                         ed25519.PrivateKey
	packageRoot                                 string
	ctx                                         context.Context
	product                                     *ent.Product
	level, auto                                 *ent.MemberLevel
	allowed, denied, plain, referral, automatic *ent.User
	command                                     plugin.Command
}

func p2Fixture(t *testing.T, driver string) *p2Env {
	t.Helper()
	source := filepath.Join(t.TempDir(), "p2.db")
	if driver != "sqlite" {
		source = os.Getenv("ZCARD_P2_" + strings.ToUpper(driver) + "_DSN")
		if source == "" {
			t.Skip("isolated P2 database not configured")
		}
	}
	d, closeDB, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeDB)
	ctx := tenancy.WithContext(context.Background(), tenancy.Main())
	if err = d.Client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	packageRoot := t.TempDir()
	store, err := plugin.NewFilePackages(packageRoot, map[string]ed25519.PublicKey{"p2-test": pub}, pc.Host{CoreVersion: "1.2.89", APIVersion: "1", Capabilities: map[string]bool{pc.HookOrderPreCreate: true}})
	if err != nil {
		t.Fatal(err)
	}
	repo := plugin.NewRepo(d, plugin.NewCoordinator(), nil)
	manager := plugin.NewManager(repo, store, plugin.NewRuntimeLoader(store))
	lv := d.Client.MemberLevel.Create().SetName("manual").SetAcquireMode("manual").SetDiscount(9000).SaveX(ctx)
	other := d.Client.MemberLevel.Create().SetName("other").SetAcquireMode("manual").SetDiscount(8000).SaveX(ctx)
	auto := d.Client.MemberLevel.Create().SetName("auto").SetThresholdType("recharge").SetThresholdRecharge(500).SetDiscount(8500).SetSort(2).SaveX(ctx)
	makeUser := func(name string) *ent.User { return d.Client.User.Create().SetUsername(name).SaveX(ctx) }
	u, bad, plain, referral, automatic := makeUser("p2-allowed"), makeUser("p2-denied"), makeUser("p2-plain"), makeUser("p2-referral"), makeUser("p2-automatic")
	d.Client.User.UpdateOneID(u.ID).SetManualLevelID(lv.ID).ExecX(ctx)
	d.Client.User.UpdateOneID(bad.ID).SetManualLevelID(other.ID).ExecX(ctx)
	d.Client.User.UpdateOneID(referral.ID).SetReferralLevelID(auto.ID).ExecX(ctx)
	d.Client.WalletTransaction.Create().SetUserID(automatic.ID).SetDirection("in").SetType("recharge").SetAmount(500).SetBalanceBefore(0).SetBalanceAfter(500).SetReference("p2-recharge").SaveX(ctx)
	p := d.Client.Product.Create().SetName("protected").SetSlug("p2-product").SetPrice(1000).SetStatus(1).SaveX(ctx)
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		plain := fmt.Sprint("p2-card-", i)
		sealed, err := cipher.Seal(plain, p.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		d.Client.Card.Create().SetProductID(p.ID).SetContent(sealed).SetContentHash(cipher.ContentHash(plain)).SaveX(ctx)
	}
	gen, err := id.NewGenerator(2)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := NewRequestFingerprinter(&conf.Data{PluginDataDir: t.TempDir()}, d)
	if err != nil {
		t.Fatal(err)
	}
	uc := &OrderUsecase{Data: d, Inv: inventory.NewCardRepoImpl(d, cipher), Gen: gen, MemberRate: memberlevel.NewMemberLevelRepoImpl(d, wallet.NewWalletRepoImpl(d)), PluginGate: plugin.NewRequiredGate(repo), Fingerprinter: fp}
	env := &p2Env{packageRoot: packageRoot, d: d, uc: uc, repo: repo, manager: manager, store: store, key: key, ctx: ctx, product: p, level: lv, auto: auto, allowed: u, denied: bad, plain: plain, referral: referral, automatic: automatic}
	script, err := os.ReadFile("../../../../examples/plugins/member-purchase-gate/main.js")
	if err != nil {
		t.Fatal(err)
	}
	env.install(t, "0.1.0", string(script), "import", 0)
	c := env.command
	c.Action = "enable"
	c.ExpectedGeneration = 1
	c.OperationID = p2UUID()
	if _, err = manager.Operate(ctx, c); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Save(ctx, pluginport.SaveConfig{Key: pluginport.RuleKey{PluginID: c.PluginID, ProductID: p.ID}, Actor: c.Actor, Expected: pluginport.Expected{Generation: 2, SchemaVersion: 1}, Config: pc.Config{SchemaVersion: 1, Revision: "0", Enabled: true, AllowedLevelIDs: []pc.Decimal{pc.Decimal(fmt.Sprint(lv.ID)), pc.Decimal(fmt.Sprint(auto.ID))}}})
	if err != nil {
		t.Fatal(err)
	}
	return env
}
func (e *p2Env) install(t *testing.T, version, script, action string, generation uint64) {
	t.Helper()
	c, err := e.importScript(t, version, script, action, generation)
	if err != nil {
		t.Fatal(err)
	}
	e.command = c
}
func (e *p2Env) importScript(t *testing.T, version, script, action string, generation uint64) (plugin.Command, error) {
	t.Helper()
	raw, err := os.ReadFile("../../platform/plugincontract/testdata/manifest-valid.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest pc.Manifest
	json.Unmarshal(raw, &manifest)
	manifest.Version = version
	raw, _ = json.Marshal(manifest)
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	for _, f := range []struct {
		name string
		b    []byte
	}{{"manifest.json", raw}, {"main.js", []byte(script)}} {
		w, _ := z.Create(f.name)
		w.Write(f.b)
	}
	z.Close()
	desc := pc.ArtifactDescriptor{SchemaVersion: 1, PluginID: manifest.ID, Version: version, ManifestSHA256: p2Digest(raw), ArchiveSHA256: p2Digest(archive.Bytes()), ArchiveBytes: pc.Decimal(strconv.Itoa(archive.Len())), KeyID: "p2-test"}
	message, err := pc.SigningMessage(desc)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, _ := json.Marshal(desc)
	c := plugin.Command{OperationID: p2UUID(), PluginID: manifest.ID, Action: action, TargetDigest: desc.ArchiveSHA256, ExpectedGeneration: generation, ApprovedScopes: manifest.Scopes, Actor: pluginport.Actor{AdminID: 1, ScopeVerified: true, InstanceAdmin: true}}
	_, err = e.manager.Import(e.ctx, c, descriptor, ed25519.Sign(e.key, message), bytes.NewReader(archive.Bytes()))
	return c, err
}
func (e *p2Env) input(user uint64) CreateOrderInput {
	return CreateOrderInput{UserID: user, GuestContact: "guest@example.test", Contact: "guest@example.test", QueryPassword: "p2-password", Items: []OrderItemInput{{ProductID: e.product.ID, Quantity: 1}}}
}
func p2Snapshot(t *testing.T, d *data.Data) string {
	t.Helper()
	tables := []string{"cards", "wallet_accounts", "wallet_transactions", "point_accounts", "point_transactions", "coupons", "orders", "order_items", "outbox_events", "procurement_orders", "product_delivery_sources"}
	var all []any
	for _, table := range tables {
		rows, err := d.DB.Query("SELECT * FROM " + table + " ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var result [][]any
		for rows.Next() {
			v := make([]any, len(cols))
			args := make([]any, len(cols))
			for i := range v {
				args[i] = &v[i]
			}
			if err = rows.Scan(args...); err != nil {
				t.Fatal(err)
			}
			result = append(result, v)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		all = append(all, result)
	}
	raw, _ := json.Marshal(all)
	return string(raw)
}

type countedMembership struct {
	inner memberport.RateResolver
	calls atomic.Int32
}

func (r *countedMembership) EffectiveRate(ctx context.Context, id uint64) (int32, uint64, error) {
	r.calls.Add(1)
	return r.inner.EffectiveRate(ctx, id)
}
func TestP2PurchaseFlow(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			e := p2Fixture(t, driver)
			for _, tc := range []struct {
				name   string
				user   uint64
				reason string
				amount int64
			}{{"guest", 0, "LOGIN_REQUIRED", 0}, {"no-level", e.plain.ID, "MEMBER_LEVEL_DENIED", 0}, {"wrong-level", e.denied.ID, "MEMBER_LEVEL_DENIED", 0}, {"manual", e.allowed.ID, "", 900}, {"referral", e.referral.ID, "", 850}, {"automatic", e.automatic.ID, "", 850}} {
				t.Run(tc.name, func(t *testing.T) {
					before := p2Snapshot(t, e.d)
					out, err := e.uc.CreateOrder(e.ctx, e.input(tc.user))
					if tc.reason != "" {
						if err == nil || !strings.Contains(err.Error(), tc.reason) {
							t.Fatalf("wrong denial: %v", err)
						}
						if before != p2Snapshot(t, e.d) {
							t.Fatal("denial changed business data")
						}
					} else {
						if err != nil || out.TotalCents != tc.amount {
							t.Fatalf("wrong price %+v %v", out, err)
						}
						row := e.d.Client.Order.Query().Where(orderent.OrderNo(out.OrderNo)).OnlyX(e.ctx)
						if len(row.PluginDecisions) != 1 || row.PluginDecisions[0]["digest"] != e.command.TargetDigest {
							t.Fatal("decision provenance absent")
						}
					}
				})
			}
			t.Run("member-snapshot", func(t *testing.T) { p2MemberSnapshot(t, e, driver) })
			t.Run("first-rule-race", func(t *testing.T) { p2FirstRuleRace(t, e) })
			t.Run("concurrent-idempotency", func(t *testing.T) { p2ConcurrentIdempotency(t, e) })
			t.Run("opposite-cart-locks", func(t *testing.T) { p2OppositeCartLocks(t, e) })
			counter := &countedMembership{inner: e.uc.MemberRate}
			e.uc.MemberRate = counter
			e.d.Client.Product.UpdateOneID(e.product.ID).SetIsLocked(true).ExecX(e.ctx)
			first := e.input(e.allowed.ID)
			first.IdempotencyKey = "p2-replay"
			original, err := e.uc.CreateOrder(e.ctx, first)
			if err != nil {
				t.Fatal("maintenance lock blocked purchase", err)
			}
			if counter.calls.Load() != 1 {
				t.Fatal("membership resolved more than once")
			}
			e.d.Client.Product.UpdateOneID(e.product.ID).SetIsLocked(false).ExecX(e.ctx)
			oldSession, err := e.uc.PluginGate.Begin(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			// A v2 script changes the outcome; the core has no hard-coded membership allow.
			e.install(t, "0.1.1", `function evaluate(input){return {allow:false,reason:'MEMBER_LEVEL_DENIED'}}`, "upgrade", 2)
			decisions, err := oldSession.Check(e.ctx, pluginport.PurchaseInput{UserID: e.allowed.ID, LevelID: e.level.ID, Channel: "storefront", Items: []pluginport.PurchaseItem{{ProductID: e.product.ID, Quantity: 1}}})
			oldSession.Release()
			if err != nil || len(decisions) != 1 || decisions[0]["generation"] != "2" {
				t.Fatal("inflight old generation lost", err)
			}
			before := p2Snapshot(t, e.d)
			if _, err = e.uc.CreateOrder(e.ctx, e.input(e.allowed.ID)); err == nil || !strings.Contains(err.Error(), "MEMBER_LEVEL_DENIED") {
				t.Fatal("upgrade did not execute new script", err)
			}
			if before != p2Snapshot(t, e.d) {
				t.Fatal("upgraded denial changed business data")
			}
			e.uc.StockGate = &fakeStockGate{err: fmt.Errorf("must not run on replay")}
			again, err := e.uc.CreateOrder(e.ctx, first)
			if err != nil || again.OrderNo != original.OrderNo {
				t.Fatal("legal replay re-evaluated new rule", err)
			}
			changed := first
			changed.Items = []OrderItemInput{{ProductID: e.product.ID, Quantity: 2}}
			if _, err = e.uc.CreateOrder(e.ctx, changed); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
				t.Fatal("parameter replay accepted", err)
			}
			e.uc.StockGate = nil
			if err = e.uc.MarkPaid(e.ctx, original.OrderNo); err != nil {
				t.Fatal("existing payment blocked", err)
			}
			// Restart uses verified immutable artifacts and keeps the exact rule.
			newRepo := plugin.NewRepo(e.d, plugin.NewCoordinator(), nil)
			restarted := plugin.NewManager(newRepo, e.store, plugin.NewRuntimeLoader(e.store))
			if err = restarted.Reconcile(e.ctx); err != nil {
				t.Fatal(err)
			}
			e.uc.PluginGate = plugin.NewRequiredGate(newRepo)
			e.repo = newRepo
			e.manager = restarted
			if _, err = e.uc.CreateOrder(e.ctx, e.input(e.allowed.ID)); err == nil || !strings.Contains(err.Error(), "MEMBER_LEVEL_DENIED") {
				t.Fatal("restart lost active gate", err)
			}
			// Missing/corrupt config is never interpreted as disabled.
			e.d.Client.PluginData.Update().Where(plugindata.PluginID(e.command.PluginID)).SetPayload([]byte("broken")).ExecX(e.ctx)
			if _, err = e.uc.CreateOrder(e.ctx, e.input(e.allowed.ID)); err == nil || !strings.Contains(err.Error(), "PLUGIN_UNAVAILABLE") {
				t.Fatal("corrupt rule bypassed", err)
			}
			if _, err = e.repo.Release(e.ctx, pluginport.ReleaseRule{Key: pluginport.RuleKey{PluginID: e.command.PluginID, ProductID: e.product.ID}, Actor: e.command.Actor, ExpectedRequirementRevision: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err = e.uc.CreateOrder(e.ctx, e.input(e.denied.ID)); err != nil {
				t.Fatal("explicit release did not restore normal purchase", err)
			}

		})
	}
}
