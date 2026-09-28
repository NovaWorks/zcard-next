package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	entbase "entgo.io/ent"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/plugindata"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

type packageFixture struct {
	descriptor, signature, archive []byte
	artifact                       port.Artifact
}

func testPackages(t *testing.T) (*FilePackages, ed25519.PrivateKey) {
	t.Helper()
	seed := sha256.Sum256([]byte("zcard P1 TEST ONLY signing key"))
	key := ed25519.NewKeyFromSeed(seed[:])
	p, err := NewFilePackages(t.TempDir(), map[string]ed25519.PublicKey{"test-artifact": key.Public().(ed25519.PublicKey)}, pc.Host{CoreVersion: "1.2.89", APIVersion: "1", Capabilities: map[string]bool{pc.HookOrderPreCreate: true}})
	if err != nil {
		t.Fatal(err)
	}
	return p, key
}
func signedPackage(t *testing.T, key ed25519.PrivateKey, version string, mutate func(map[string][]byte)) packageFixture {
	return signedPackageID(t, key, "member-purchase-gate", version, mutate)
}
func signedPackageID(t *testing.T, key ed25519.PrivateKey, id, version string, mutate func(map[string][]byte)) packageFixture {
	t.Helper()
	raw, err := os.ReadFile("../../platform/plugincontract/testdata/manifest-valid.json")
	if err != nil {
		t.Fatal(err)
	}
	var m pc.Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	m.ID = id
	raw, _ = json.Marshal(m)
	files := map[string][]byte{"manifest.json": raw, "main.js": []byte("function evaluate(input) { return {allow:true,reason:'OK'}; }")}
	if mutate != nil {
		mutate(files)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, b := range files {
		f, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	d := pc.ArtifactDescriptor{SchemaVersion: 1, PluginID: m.ID, Version: m.Version, ManifestSHA256: pcDigest(files["manifest.json"]), ArchiveSHA256: pcDigest(buf.Bytes()), ArchiveBytes: pc.Decimal(strconv.Itoa(buf.Len())), KeyID: "test-artifact"}
	desc, _ := json.Marshal(d)
	msg, e := pc.SigningMessage(d)
	if e != nil {
		t.Fatal(e)
	}
	return packageFixture{desc, ed25519.Sign(key, msg), buf.Bytes(), port.Artifact{Descriptor: d, Manifest: m}}
}
func pcDigest(b []byte) string { return digest(b) }
func TestSignedPackages(t *testing.T) {
	p, key := testPackages(t)
	ctx := context.Background()
	f := signedPackage(t, key, "0.1.0", nil)
	if _, e := p.Stage(ctx, f.descriptor, f.signature, bytes.NewReader(f.archive)); e != nil {
		t.Fatal(e)
	}
	a, r, e := p.Open(ctx, f.artifact.Descriptor.ArchiveSHA256)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if a.Manifest.Version != "0.1.0" || !bytes.Equal(got, f.archive) {
		t.Fatal("artifact changed")
	}
	for _, tc := range []struct {
		name    string
		fixture packageFixture
	}{
		{"traversal", signedPackage(t, key, "0.1.1", func(m map[string][]byte) { delete(m, "main.js"); m["../main.js"] = []byte("bad") })},
		{"extra", signedPackage(t, key, "0.1.1", func(m map[string][]byte) { m["other.js"] = []byte("bad") })},
		{"oversize-script", signedPackage(t, key, "0.1.1", func(m map[string][]byte) { m["main.js"] = bytes.Repeat([]byte("x"), pc.MaxScriptBytes+1) })},
		{"missing", signedPackage(t, key, "0.1.1", func(m map[string][]byte) { delete(m, "main.js") })},
		{"invalid-manifest", signedPackage(t, key, "0.1.1", func(m map[string][]byte) { m["manifest.json"] = []byte(`{"id":"evil"}`) })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fixture
			if _, e := p.Stage(ctx, f.descriptor, f.signature, bytes.NewReader(f.archive)); e == nil {
				t.Fatal("bad package accepted")
			}
		})
	}
	badSig := bytes.Clone(f.signature)
	badSig[0] ^= 1
	if _, e := p.Stage(ctx, f.descriptor, badSig, bytes.NewReader(f.archive)); e == nil {
		t.Fatal("bad signature accepted")
	}
	changed := bytes.Clone(f.archive)
	changed[len(changed)-1] ^= 1
	if _, e := p.Stage(ctx, f.descriptor, f.signature, bytes.NewReader(changed)); e == nil {
		t.Fatal("tamper accepted")
	}
	path := filepath.Join(p.root, a.Manifest.ID, a.Descriptor.ArchiveSHA256, "archive.zip")
	if e := os.WriteFile(path, changed, 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = p.Open(ctx, a.Descriptor.ArchiveSHA256); e == nil {
		t.Fatal("corrupt stored archive accepted")
	}
}

type testRuntime struct{ closed atomic.Bool }

func (r *testRuntime) Evaluate(context.Context, pc.Input) (pc.Decision, error) {
	return pc.Decision{Allow: true, Reason: pc.OK}, nil
}
func (r *testRuntime) Close() error { r.closed.Store(true); return nil }

type testLoader struct {
	fail   bool
	latest *testRuntime
}

func (l *testLoader) Prepare(context.Context, port.Artifact) (port.PreparedRuntime, error) {
	if l.fail {
		return nil, errors.New("fixture init failure")
	}
	l.latest = &testRuntime{}
	return l.latest, nil
}

func command(f packageFixture, action string, generation uint64) Command {
	return Command{OperationID: randomOperationID(), PluginID: f.artifact.Manifest.ID, Action: action, TargetDigest: f.artifact.Descriptor.ArchiveSHA256, ExpectedGeneration: generation, ApprovedScopes: f.artifact.Manifest.Scopes, Actor: port.Actor{AdminID: 1, SubsiteID: 0, ScopeVerified: true, InstanceAdmin: true}}
}
func TestFoundation(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "plugin.db")
			if driver != "sqlite" {
				source = os.Getenv("ZCARD_P1_" + strings.ToUpper(driver) + "_DSN")
				if source == "" {
					t.Skip("isolated P1 database not configured")
				}
			}
			d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			ctx := tenancy.WithContext(context.Background(), tenancy.Main())
			if err = d.Client.Schema.Create(ctx); err != nil {
				t.Fatal(err)
			}
			p, key := testPackages(t)
			testID := "member-purchase-gate-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			f := signedPackageID(t, key, testID, "0.1.0", nil)
			coord := NewCoordinator()
			repo := NewRepo(d, coord, nil)
			loader := &testLoader{}
			m := NewManager(repo, p, loader)
			imp := command(f, "import", 0)
			op, err := m.Import(ctx, imp, f.descriptor, f.signature, bytes.NewReader(f.archive))
			if err != nil || op.Phase != port.PhaseReconciled {
				t.Fatalf("import: %v %v", op, err)
			}
			if _, e := coord.Acquire(ctx, f.artifact.Manifest.ID); e == nil {
				t.Fatal("import activated runtime")
			}
			production := NewManager(repo, p, nil)
			if _, e := production.Operate(ctx, command(f, "enable", 1)); e == nil {
				t.Fatal("P1 production activated test runtime")
			}
			if _, err = m.Operate(ctx, command(f, "enable", 1)); err != nil {
				t.Fatal(err)
			}
			verifyFirstRuleBoundary(t, ctx, d, repo, testID, imp.Actor)
			oldRuntime := loader.latest
			lease, err := coord.Acquire(ctx, f.artifact.Manifest.ID)
			if err != nil {
				t.Fatal(err)
			}
			productRow := d.Client.Product.Create().SetName("P1").SetSlug(testID).SetPrice(100).SaveX(ctx)
			level := d.Client.MemberLevel.Create().SetName("P1 level").SetThresholdType("recharge").SetThresholdRecharge(0).SetThresholdConsume(0).SaveX(ctx)
			k := port.RuleKey{PluginID: f.artifact.Manifest.ID, ProductID: productRow.ID}
			actor := imp.Actor
			save := port.SaveConfig{Key: k, Actor: actor, Expected: port.Expected{Generation: 2, SchemaVersion: 1}, Config: pc.Config{SchemaVersion: 1, Revision: "0", Enabled: true, AllowedLevelIDs: []pc.Decimal{pc.Decimal(strconv.FormatUint(level.ID, 10))}}}
			rule, err := repo.Save(ctx, save)
			if err != nil {
				t.Fatal(err)
			}
			if !rule.Requirement.Required || rule.Requirement.Revision != 1 {
				t.Fatal("requirement not committed")
			}
			referenced, err := repo.LevelReferenced(ctx, level.ID)
			if err != nil || !referenced {
				t.Fatal("level reference missing")
			}
			if _, err = repo.Save(ctx, save); err == nil {
				t.Fatal("stale config accepted")
			}
			other := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 99})
			if _, err = repo.Get(other, k); err == nil {
				t.Fatal("cross-site read allowed")
			}
			if _, err = repo.Get(context.Background(), k); err == nil {
				t.Fatal("implicit main-site fallback")
			}
			d.Client.Product.UpdateOneID(productRow.ID).SetIsLocked(true).ExecX(ctx)
			if _, err = repo.Release(ctx, port.ReleaseRule{Key: k, Actor: actor, ExpectedRequirementRevision: 1}); err == nil {
				t.Fatal("locked product changed")
			}
			d.Client.Product.UpdateOneID(productRow.ID).SetIsLocked(false).ExecX(ctx)
			// Prepare failure must retain the active runtime and desired generation.
			f2 := signedPackageID(t, key, testID, "0.1.1", nil)
			if _, err = p.Stage(ctx, f2.descriptor, f2.signature, bytes.NewReader(f2.archive)); err != nil {
				t.Fatal(err)
			}
			loader.fail = true
			if _, err = m.Operate(ctx, command(f2, "upgrade", 2)); err == nil {
				t.Fatal("prepare failure accepted")
			}
			state, _ := m.Status(ctx, k.PluginID)
			if state.State.DesiredGeneration != 2 {
				t.Fatal("prepare failure changed intent")
			}
			loader.fail = false
			var failIntent atomic.Bool
			failIntent.Store(true)
			d.Client.InstalledPlugin.Use(func(next entbase.Mutator) entbase.Mutator {
				return entbase.MutateFunc(func(ctx context.Context, mutation entbase.Mutation) (entbase.Value, error) {
					if _, ok := mutation.Field("desired_generation"); ok && mutation.Op().Is(entbase.OpUpdate) && failIntent.CompareAndSwap(true, false) {
						return nil, errors.New("fixture intent write failure")
					}
					return next.Mutate(ctx, mutation)
				})
			})
			if _, err = m.Operate(ctx, command(f2, "upgrade", 2)); err == nil {
				t.Fatal("intent failure accepted")
			}
			state, err = m.Status(ctx, k.PluginID)
			if err != nil || state.State.DesiredGeneration != 2 || state.State.ObservedGeneration != 2 || oldRuntime.closed.Load() || !loader.latest.closed.Load() {
				t.Fatalf("intent failure changed old runtime: %+v %v", state, err)
			}
			// Inject the final DB confirmation failure, after publication.
			var failConfirm atomic.Bool
			failConfirm.Store(true)
			d.Client.InstalledPlugin.Use(func(next entbase.Mutator) entbase.Mutator {
				return entbase.MutateFunc(func(ctx context.Context, mutation entbase.Mutation) (entbase.Value, error) {
					if _, ok := mutation.Field("observed_generation"); ok && mutation.Op().Is(entbase.OpUpdateOne) && failConfirm.CompareAndSwap(true, false) {
						return nil, errors.New("fixture confirmation failure")
					}
					return next.Mutate(ctx, mutation)
				})
			})
			upgrade := command(f2, "upgrade", 2)
			if _, err = m.Operate(ctx, upgrade); err == nil {
				t.Fatal("confirmation failure hidden")
			}
			state, _ = m.Status(ctx, k.PluginID)
			if state.State.ObservedGeneration != 3 || !state.State.ReconciliationPending {
				t.Fatalf("published version misreported: %+v", state)
			}
			if oldRuntime.closed.Load() {
				t.Fatal("in-flight runtime closed")
			}
			lease.Release()
			if !oldRuntime.closed.Load() {
				t.Fatal("old runtime not drained")
			}
			if _, err = m.Operate(ctx, upgrade); err != nil {
				t.Fatal(err)
			}
			state, _ = m.Status(ctx, k.PluginID)
			if state.State.ReconciliationPending || state.State.DesiredGeneration != 3 {
				t.Fatal("retry not reconciled")
			}
			changed := upgrade
			changed.Action = "rollback"
			if _, err = m.Operate(ctx, changed); err == nil {
				t.Fatal("operation ID parameter reuse accepted")
			}
			// Save versus save: exactly one CAS wins, independently of DB dialect.
			save.Expected = port.Expected{Generation: 3, SchemaVersion: 1, ConfigRevision: 1, RequirementRevision: 1}
			save.Config.Revision = "1"
			var wins atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, e := repo.Save(ctx, save); e == nil {
						wins.Add(1)
					}
				}()
			}
			wg.Wait()
			if wins.Load() != 1 {
				t.Fatalf("CAS winners=%d", wins.Load())
			}
			var failRef atomic.Bool
			failRef.Store(true)
			d.Client.PluginRuleLevelRef.Use(func(next entbase.Mutator) entbase.Mutator {
				return entbase.MutateFunc(func(ctx context.Context, mutation entbase.Mutation) (entbase.Value, error) {
					if mutation.Op().Is(entbase.OpCreate) && failRef.CompareAndSwap(true, false) {
						return nil, errors.New("fixture reference write failure")
					}
					return next.Mutate(ctx, mutation)
				})
			})
			save.Expected.ConfigRevision = 2
			save.Expected.RequirementRevision = 2
			save.Config.Revision = "2"
			if _, err = repo.Save(ctx, save); err == nil {
				t.Fatal("reference failure accepted")
			}
			after, e := repo.Get(ctx, k)
			if e != nil || after.Config.Revision != "2" || after.Requirement.Revision != 2 {
				t.Fatalf("partial config write: %+v %v", after, e)
			}
			disable := command(f2, "disable", 3)
			disable.TargetDigest = ""
			if _, err = m.Operate(ctx, disable); err != nil {
				t.Fatal(err)
			}
			reqs, err := repo.ListForProducts(ctx, 0, []uint64{k.ProductID})
			if err != nil || len(reqs) != 1 || !reqs[0].Required {
				t.Fatal("disable erased requirement")
			}
			if _, err = coord.Acquire(ctx, k.PluginID); err == nil {
				t.Fatal("disabled runtime available")
			}
			// Corrupt JSON and remove artifacts: host release still works and keeps CAS tombstone.
			d.Client.PluginData.Update().Where(plugindata.PluginID(k.PluginID)).SetPayload([]byte("broken")).ExecX(ctx)
			if _, err = repo.Get(ctx, k); err == nil {
				t.Fatal("corrupt config accepted")
			}
			if err = os.RemoveAll(filepath.Join(p.root, k.PluginID)); err != nil {
				t.Fatal(err)
			}
			if _, err = repo.Release(ctx, port.ReleaseRule{Key: k, Actor: actor, ExpectedRequirementRevision: 2}); err != nil {
				t.Fatal(err)
			}
			referenced, err = repo.LevelReferenced(ctx, level.ID)
			if err != nil || referenced {
				t.Fatal("release left references")
			}
			if v, e := repo.Get(ctx, k); e != nil || v.Requirement.Required || v.Config.Enabled || v.Requirement.Revision != 3 {
				t.Fatalf("release: %+v %v", v, e)
			}
			// Explicit isolation wrappers reject accidental nesting under the old transaction helper.
			if err = data.Tx(ctx, d, func(ctx context.Context) error {
				return data.PurchaseTx(ctx, d, func(context.Context) error { return nil })
			}); err == nil {
				t.Fatal("unknown nested isolation accepted")
			}
			if err = data.PurchaseTx(ctx, d, func(ctx context.Context) error {
				return data.PurchaseTx(ctx, d, func(context.Context) error { return nil })
			}); err != nil {
				t.Fatal(err)
			}
			// Required writes are not published if a transaction fails mid-save.

		})
	}
}

func randomOperationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	v := hex.EncodeToString(b[:])
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
}
