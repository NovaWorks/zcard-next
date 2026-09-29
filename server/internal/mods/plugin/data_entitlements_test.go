package plugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	pl "github.com/NovaWorks/zcard-next/server/internal/platform/pluginlicense"
)

type fixedIdentity string

func (id fixedIdentity) InstanceID(context.Context) (string, error) { return string(id), nil }

func TestPaidLifecycleExpiryRenewalAndOfflineRestart(t *testing.T) {
	s, d, ctx, free := serviceFixture(t)
	m := s.manager
	p := m.packages.(*FilePackages)
	p.host.PaidEntitlements = true
	m.loader = NewRuntimeLoader(p)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	policy := func() *licensePolicy {
		return &licensePolicy{identity: fixedIdentity("instance-0001"), issuer: "https://market.example", roots: map[string]pl.Root{"license-test": {Issuer: "https://market.example", PublicKey: pub}}, state: pl.NewState(), now: func() time.Time { return now }}
	}
	m.licensing = policy()
	if err = m.loadLicensing(ctx); err != nil {
		t.Fatal(err)
	}
	core := []byte(`"core license must not be overwritten"`)
	d.Client.Setting.Create().SetGroup("license").SetKey("file").SetValue(core).SaveX(ctx)
	if _, err = m.Operate(ctx, command(free, "enable", 1)); err != nil {
		t.Fatal(err)
	}
	_, artifactKey := testPackages(t)
	paid := signedPackage(t, artifactKey, "0.2.0", func(files map[string][]byte) {
		var v pc.Manifest
		if e := json.Unmarshal(files["manifest.json"], &v); e != nil {
			t.Fatal(e)
		}
		v.Entitlement.Mode = "paid"
		files["manifest.json"], _ = json.Marshal(v)
		files["main.js"] = []byte(`function evaluate(input){if(input.member.effectiveLevelId==='999')throw new Error('fault');return {allow:true,reason:'OK'}}`)
	})
	upgrade := func(confirm bool) error {
		c := command(paid, "upgrade", 2)
		c.ConfirmPaid = confirm
		_, e := m.Import(ctx, c, paid.descriptor, paid.signature, bytes.NewReader(paid.archive))
		return e
	}
	if err = upgrade(true); err == nil {
		t.Fatal("unlicensed paid upgrade")
	}
	state, err := m.Status(ctx, free.artifact.Manifest.ID)
	if err != nil || state.DesiredDigest != free.artifact.Descriptor.ArchiveSHA256 || state.State.DesiredGeneration != 2 {
		t.Fatal("old free state lost", state, err)
	}
	lease, err := m.coordinator.Acquire(ctx, free.artifact.Manifest.ID)
	if err != nil {
		t.Fatal("old free stopped", err)
	}
	lease.Release()
	l := pl.License{SchemaVersion: 1, KeyID: "license-test", Issuer: "https://market.example", LicenseID: "license-one", PluginID: free.artifact.Manifest.ID, InstanceID: "instance-0001", Revision: "1", IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Unix() + 60, Status: "active", MinVersion: "0.1.0", MaxVersionExclusive: "1.0.0"}
	install := func(l pl.License) error {
		e, err := pl.Sign(l, key)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(e)
		return m.installSecurity(ctx, raw, false, LocalActor())
	}
	if err = install(l); err != nil {
		t.Fatal(err)
	}
	if err = upgrade(false); err == nil {
		t.Fatal("missing paid confirmation")
	}
	if err = upgrade(true); err != nil {
		t.Fatal(err)
	}
	oldLease, err := m.coordinator.Acquire(ctx, l.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	gate := NewRequiredGate(m.repo)
	now = time.Unix(l.ExpiresAt, 0)
	if _, err = m.coordinator.Acquire(ctx, l.PluginID); err == nil {
		t.Fatal("expiry boundary bypass")
	}
	session, err := gate.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if session.(*purchaseSession).leases[l.PluginID] != nil {
		t.Fatal("order snapshot bypassed expiry")
	}
	session.Release()
	if _, err = oldLease.Evaluate(ctx, pc.Input{SchemaVersion: 1, Hook: pc.HookOrderPreCreate, PluginID: l.PluginID, Generation: "3", SubsiteID: "0", ProductID: "1", SKUID: "0", Quantity: 1, Channel: "storefront", Member: pc.Member{Authenticated: true, EffectiveLevelID: "1"}, Config: pc.Config{SchemaVersion: 1, Revision: "1", Enabled: true, AllowedLevelIDs: []pc.Decimal{"1"}}}); err != nil {
		t.Fatal("already acquired operation stopped", err)
	}
	oldLease.Release()
	status, err := m.Status(ctx, l.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.State.BlockReasons) != 1 || status.State.BlockReasons[0] != port.BlockReason(pl.Expired) {
		t.Fatal("expiry diagnostics", status)
	}
	old := l
	l.Revision = "2"
	l.IssuedAt = now.Unix()
	l.NotBefore = now.Unix()
	l.ExpiresAt = now.Unix() + 600
	if err = install(l); err != nil {
		t.Fatal(err)
	}
	lease, err = m.coordinator.Acquire(ctx, l.PluginID)
	if err != nil {
		t.Fatal("renewal did not resume", err)
	}
	lease.Release()
	if err = install(old); err == nil {
		t.Fatal("revision rolled back")
	}
	coreRow := d.Client.Setting.Query().Where(setting.Group("license"), setting.Key("file")).OnlyX(ctx)
	if !bytes.Equal(coreRow.Value, core) {
		t.Fatal("core license overwritten")
	}
	// A restart uses only durable signed state, with no HTTP market dependency.
	restarted := NewManager(NewRepo(d, NewCoordinator(), nil), p, NewRuntimeLoader(p))
	restarted.licensing = policy()
	if err = restarted.loadLicensing(ctx); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	lease, err = restarted.coordinator.Acquire(ctx, l.PluginID)
	if err != nil {
		t.Fatal("offline restart", err)
	}
	lease.Release()
	rt := m.coordinator.slots[l.PluginID].runtime.(*artifactRuntime)
	badInput := pc.Input{SchemaVersion: 1, Hook: pc.HookOrderPreCreate, PluginID: l.PluginID, Generation: "3", SubsiteID: "0", ProductID: "1", SKUID: "0", Quantity: 1, Channel: "storefront", Member: pc.Member{Authenticated: true, EffectiveLevelID: "999"}, Config: pc.Config{SchemaVersion: 1, Revision: "1", Enabled: true, AllowedLevelIDs: []pc.Decimal{"1"}}}
	for i := 0; i < 3; i++ {
		if _, err = rt.Evaluate(ctx, badInput); err == nil {
			t.Fatal("expected runtime fault")
		}
	}
	if !rt.Faulted() {
		t.Fatal("fault fixture did not open circuit")
	}
	l.Revision = "3"
	l.ExpiresAt++
	if err = install(l); err != nil {
		t.Fatal(err)
	}
	if !rt.Faulted() || m.coordinator.slots[l.PluginID].runtime != rt {
		t.Fatal("renewal cleared runtime fault or reloaded it")
	}
	disable := command(paid, "disable", 3)
	disable.TargetDigest = ""
	if _, err = m.Operate(ctx, disable); err != nil {
		t.Fatal(err)
	}
	l.Revision = "4"
	l.ExpiresAt++
	if err = install(l); err != nil {
		t.Fatal(err)
	}
	if _, err = m.coordinator.Acquire(ctx, l.PluginID); err == nil {
		t.Fatal("renewal enabled manually disabled plugin")
	}
	// Safety policy is orthogonal to entitlement, including for old free ZIPs.
	list := pl.RevocationList{SchemaVersion: 1, Issuer: l.Issuer, KeyID: l.KeyID, Sequence: "1", IssuedAt: now.Unix(), Entries: []pl.Revocation{{PluginID: l.PluginID, Reason: "unsafe implementation"}}}
	env, err := pl.SignRevocations(list, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(env)
	if err = m.installSecurity(ctx, raw, true, LocalActor()); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Operate(ctx, command(paid, "enable", 4)); err == nil {
		t.Fatal("renewal bypassed safety policy")
	}
	rollback := command(free, "rollback", 4)
	if _, err = m.Operate(ctx, rollback); err == nil {
		t.Fatal("rollback erased safety revocation")
	}
}

func TestEntitlementPersistenceFailureDoesNotPublish(t *testing.T) {
	s, d, ctx, _ := serviceFixture(t)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	s.manager.licensing = &licensePolicy{identity: fixedIdentity("instance-0001"), issuer: "https://market.example", roots: map[string]pl.Root{"test": {Issuer: "https://market.example", PublicKey: pub}}, now: func() time.Time { return now }}
	if err := s.manager.loadLicensing(ctx); err != nil {
		t.Fatal(err)
	}
	l := pl.License{SchemaVersion: 1, KeyID: "test", Issuer: "https://market.example", LicenseID: "one", PluginID: "plugin-a", InstanceID: "instance-0001", Revision: "1", IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Unix() + 60, Status: "active", MinVersion: "0.1.0", MaxVersionExclusive: "1.0.0"}
	e, err := pl.Sign(l, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	if err := s.manager.installSecurity(ctx, raw, false, port.Actor{}); err == nil {
		t.Fatal("unauthorized")
	}
	d.DB.Close()
	if err := s.manager.installSecurity(ctx, raw, false, LocalActor()); err == nil {
		t.Fatal("lost write accepted")
	}
	if len(s.manager.licensing.state.Licenses) != 0 {
		t.Fatal("authorization published before persistence")
	}
}
