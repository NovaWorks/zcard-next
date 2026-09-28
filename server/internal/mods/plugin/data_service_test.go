package plugin

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/pluginstorage"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testAuthorizer map[string]bool

func (a testAuthorizer) Allowed(_ context.Context, _ uint64, permission string) bool {
	return a[permission]
}
func (a testAuthorizer) PermissionsOf(context.Context, uint64) ([]string, error) { return nil, nil }
func (a testAuthorizer) RoleName(context.Context, uint64) string                 { return "超级管理员" }
func (a testAuthorizer) RoleCode(_ context.Context, id uint64) string {
	if id == 1 {
		return "super_admin"
	}
	return "operator"
}
func serviceFixture(t *testing.T) (*AdminPluginService, *data.Data, context.Context, packageFixture) {
	t.Helper()
	d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: filepath.Join(t.TempDir(), "api.db")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	ctx := tenancy.WithContext(authn.WithClaims(context.Background(), &authn.Claims{Subject: 1, RoleID: 1, Realm: authn.RealmAdmin}), tenancy.Main())
	if err = d.Client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	p, key := testPackages(t)
	f := signedPackage(t, key, "0.1.0", nil)
	repo := NewRepo(d, NewCoordinator(), nil)
	m := NewManager(repo, p, nil)
	if _, err = m.Import(ctx, command(f, "import", 0), f.descriptor, f.signature, bytes.NewReader(f.archive)); err != nil {
		t.Fatal(err)
	}
	s := NewAdminPluginService(m, testAuthorizer{"plugin:read": true, "plugin:configure": true, "plugin:release": true, "plugin:manage": true, "catalog:write": true})
	return s, d, ctx, f
}
func TestAdminScopeAndUnavailableRuntime(t *testing.T) {
	s, d, ctx, f := serviceFixture(t)
	p := d.Client.Product.Create().SetName("test").SetSlug("test").SetPrice(100).SaveX(ctx)
	request := &adminv1.ProductPluginRequest{ProductId: strconv.FormatUint(p.ID, 10), PluginId: f.artifact.Manifest.ID}
	if _, err := s.GetProductRule(context.Background(), request); kerrors.Code(err) != 401 {
		t.Fatalf("anonymous=%v", err)
	}
	noTenant := authn.WithClaims(context.Background(), authn.ClaimsFromContext(ctx))
	if _, err := s.GetProductRule(noTenant, request); kerrors.Code(err) != 403 {
		t.Fatalf("missing scope=%v", err)
	}
	other := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 99})
	if _, err := s.GetProductRule(other, request); kerrors.Code(err) != 403 {
		t.Fatalf("cross-site=%v", err)
	}
	missingCatalog := NewAdminPluginService(s.manager, testAuthorizer{"plugin:read": true})
	if _, err := missingCatalog.GetProductRule(ctx, request); kerrors.Code(err) != 403 {
		t.Fatalf("permission intersection=%v", err)
	}
	if out, err := s.GetProductSchema(ctx, request); err != nil || out.RuntimeAvailable || out.ConfigSchemaJson == "" {
		t.Fatalf("schema=%v %v", out, err)
	}
	if out, err := s.ListContributions(ctx, request); err != nil || len(out.Contributions) != 0 || len(out.UnavailableReasons) == 0 {
		t.Fatalf("fake active contribution=%v %v", out, err)
	}
	if _, err := s.ListPlugins(ctx, &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	actor := authn.WithClaims(ctx, &authn.Claims{Subject: 2, RoleID: 2, Realm: authn.RealmAdmin})
	if _, err := s.OperatePlugin(actor, &adminv1.OperatePluginRequest{PluginId: request.PluginId, OperationId: randomOperationID(), Action: "disable", ExpectedGeneration: "1"}); kerrors.Code(err) != 403 {
		t.Fatalf("instance control=%v", err)
	}
	if _, err := s.SaveProductRule(ctx, &adminv1.SaveProductRuleRequest{PluginId: request.PluginId, ProductId: request.ProductId, ExpectedGeneration: "1", ExpectedConfigRevision: "0", ExpectedRequirementRevision: "0", SchemaVersion: 1, Config: &adminv1.PluginConfig{SchemaVersion: 1, Revision: "0", Enabled: true, AllowedLevelIds: []string{"7"}}}); kerrors.Code(err) != 503 {
		t.Fatalf("P1 enabled fake rule=%v", err)
	}
}
func TestLocalControlAndOfflineExclusion(t *testing.T) {
	s, _, ctx, f := serviceFixture(t)
	root, err := os.MkdirTemp("/tmp", "zcard-p1-control-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	lock, err := pluginstorage.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	closeControl, err := s.manager.StartControl(root)
	if err != nil {
		t.Fatal(err)
	}
	defer closeControl()
	if _, err = pluginstorage.Acquire(root); !errors.Is(err, pluginstorage.ErrLocked) {
		t.Fatalf("offline writer raced live process: %v", err)
	}
	c := command(f, "disable", 1)
	c.TargetDigest = ""
	reply, err := SendControl(ctx, root, ControlRequest{Action: "operate", Command: c})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Operation == nil || reply.Status.State.DesiredEnabled || reply.Status.State.ObservedGeneration != 2 {
		t.Fatalf("disable not observed: %+v", reply)
	}
	again, err := SendControl(ctx, root, ControlRequest{Action: "operate", Command: c})
	if err != nil || again.Status.State.DesiredGeneration != 2 {
		t.Fatalf("duplicate command: %+v %v", again, err)
	}
	info, err := os.Stat(filepath.Join(root, ".plugin-control"))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("control directory not private")
	}
	info, err = os.Stat(filepath.Join(root, ".plugin-control", "socket"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("control socket not private")
	}
}

func TestReconcileMissingArtifactAndOperatorIntent(t *testing.T) {
	s, _, ctx, f := serviceFixture(t)
	m := s.manager
	fresh := NewManager(NewRepo(m.repo.data, NewCoordinator(), nil), m.packages, nil)
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := fresh.Status(ctx, f.artifact.Manifest.ID)
	if err != nil || state.State.DesiredEnabled || state.State.ObservedGeneration != 1 {
		t.Fatalf("disabled intent changed: %+v %v", state, err)
	}
	store := m.packages.(*FilePackages)
	if err = os.RemoveAll(filepath.Join(store.root, f.artifact.Manifest.ID)); err != nil {
		t.Fatal(err)
	}
	broken := NewManager(NewRepo(m.repo.data, NewCoordinator(), nil), m.packages, nil)
	if err = broken.Reconcile(ctx); err != nil {
		t.Fatal("one corrupt plugin stopped startup", err)
	}
	state, err = broken.Status(ctx, f.artifact.Manifest.ID)
	if err != nil || state.State.ObservedGeneration != 0 || !state.State.ReconciliationPending {
		t.Fatalf("missing artifact reported running: %+v %v", state, err)
	}
	disable := command(f, "disable", 1)
	disable.TargetDigest = ""
	if _, err = broken.Operate(ctx, disable); err != nil {
		t.Fatal("cannot disable missing plugin", err)
	}
	state, err = broken.Status(ctx, f.artifact.Manifest.ID)
	if err != nil || state.State.DesiredEnabled || state.State.ObservedGeneration != 2 {
		t.Fatalf("disable not applied: %+v %v", state, err)
	}
}

func TestP1RefusesServingRestoredActiveRequirements(t *testing.T) {
	s, d, ctx, f := serviceFixture(t)
	if err := s.manager.ValidateServing(ctx); err != nil {
		t.Fatal(err)
	}
	d.Client.PluginRequirement.Create().SetPluginID(f.artifact.Manifest.ID).SetProductID(42).SetSubsiteID(0).SetRequired(true).SetRevision(1).SaveX(ctx)
	if err := s.manager.ValidateServing(ctx); err == nil {
		t.Fatal("P1 served rules without purchase enforcement")
	}
	if err := s.manager.ValidateSplitMode(ctx); err == nil {
		t.Fatal("split mode bypassed active requirements")
	}
	// Maintenance remains possible under the offline instance lock.
	c := command(f, "disable", 1)
	c.TargetDigest = ""
	if _, err := s.manager.Operate(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.manager.ValidateServing(ctx); err == nil {
		t.Fatal("disable silently removed purchase requirement")
	}
}
