package plugin

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	mb "github.com/NovaWorks/zcard-next/server/internal/platform/marketbinding"
	pl "github.com/NovaWorks/zcard-next/server/internal/platform/pluginlicense"
)

func TestBindingDurableRecoveryAndAtomicSync(t *testing.T) {
	s, _, ctx, _ := serviceFixture(t)
	m := s.manager
	box, e := crypto.NewBox(bytes.Repeat([]byte{8}, 32))
	if e != nil {
		t.Fatal(e)
	}
	m.bindingBox = box
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	random := func() string {
		v, e := mb.Random()
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	token1, token2, device := random(), random(), random()
	var mu sync.Mutex
	calls := map[string]int{}
	var request mb.Start
	rotationID := ""
	var envelopes []pl.Envelope
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		action := strings.TrimPrefix(r.URL.Path, mb.Prefix+"/")
		calls[action]++
		switch action {
		case "start":
			var in mb.Start
			json.NewDecoder(r.Body).Decode(&in)
			if calls[action] == 1 {
				request = in
				http.Error(w, "lost response", 503)
				return
			}
			if in != request {
				t.Error("start recovery replaced request")
			}
			json.NewEncoder(w).Encode(mb.Pair{PairID: in.RequestID, DeviceSecret: device, UserCode: "ab0123456789", ExpiresAt: time.Now().Add(9 * time.Minute).Unix(), PollSeconds: 5})
		case "poll":
			json.NewEncoder(w).Encode(mb.PairStatus{Status: "approved", AccountID: "17", AccountName: "buyer", ExpiresAt: time.Now().Add(9 * time.Minute).Unix()})
		case "confirm":
			var in mb.Proof
			json.NewDecoder(r.Body).Decode(&in)
			if in.PairID != request.RequestID || in.DeviceSecret != device || in.AccountID != "17" {
				t.Error("confirm proof")
			}
			if calls[action] == 1 {
				http.Error(w, "lost response", 503)
				return
			}
			json.NewEncoder(w).Encode(mb.Credential{Token: token1, AccountID: "17", AccountName: "buyer", Version: "1", ExpiresAt: time.Now().Add(29 * 24 * time.Hour).Unix()})
		case "rotate":
			var in mb.Operation
			json.NewDecoder(r.Body).Decode(&in)
			if calls[action] == 1 {
				rotationID = in.RequestID
				http.Error(w, "lost response", 503)
				return
			}
			if in.RequestID != rotationID || r.Header.Get("Authorization") != "Bearer "+token1 {
				t.Error("rotation recovery changed identity")
			}
			json.NewEncoder(w).Encode(mb.Credential{Token: token2, AccountID: "17", AccountName: "buyer", Version: "2", ExpiresAt: time.Now().Add(29 * 24 * time.Hour).Unix()})
		case "sync":
			if r.Header.Get("Authorization") != "Bearer "+token2 {
				t.Error("sync used old credential")
			}
			json.NewEncoder(w).Encode(mb.Sync{Licenses: envelopes})
		case "revoke":
			if calls[action] == 1 {
				http.Error(w, "lost response", 503)
				return
			}
			fmt.Fprint(w, `{"revoked":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("ZCARD_MARKET_TEST_ORIGIN", server.URL)
	// Production issuers remain HTTPS even when the explicit fixture transport is HTTP.
	issuer := "https://market.example"
	m.licensing = &licensePolicy{identity: fixedIdentity("instance-binding"), issuer: issuer, roots: map[string]pl.Root{"license-test": {Issuer: issuer, PublicKey: pub}}, state: pl.NewState(), now: time.Now}
	if e = m.loadLicensing(ctx); e != nil {
		t.Fatal(e)
	}
	if e = m.repo.configureMarket(ctx, server.URL); e != nil {
		t.Fatal(e)
	}
	act := func(action string, confirm bool) (bindingView, error) {
		return m.bindingOperation(ctx, bindingAction{Action: action, AccountID: "17", Confirm: confirm}, LocalActor())
	}
	if _, e = act("start", false); e == nil {
		t.Fatal("lost start not reported")
	}
	if v, e := m.bindingStatus(ctx); e != nil || v.State != "starting" {
		t.Fatal(v, e)
	}
	if e = m.repo.configureMarket(ctx, "https://other.example"); e == nil {
		t.Fatal("changed origin with pending secrets")
	}
	if _, e = act("start", false); e != nil {
		t.Fatal(e)
	}
	if _, e = act("confirm", true); e == nil {
		t.Fatal("confirmed before account approval")
	}
	if _, e = act("poll", false); e != nil {
		t.Fatal(e)
	}
	if _, e = act("confirm", false); e == nil {
		t.Fatal("implicit confirmation")
	}
	if _, e = act("confirm", true); e == nil {
		t.Fatal("lost confirmation not reported")
	}
	if _, e = act("cancel", false); e == nil {
		t.Fatal("cancelled uncertain confirmation")
	}
	if v, e := act("confirm", true); e != nil || v.State != "bound" {
		t.Fatal(v, e)
	}
	if _, e = act("rotate", false); e == nil {
		t.Fatal("lost rotation not reported")
	}
	if _, e = act("revoke", true); e == nil {
		t.Fatal("revoked while rotation uncertain")
	}
	// New manager with the same persistent store simulates a host restart.
	restarted := NewManager(m.repo, m.packages, m.loader)
	restarted.bindingBox = box
	restarted.licensing = m.licensing
	m = restarted
	if v, e := act("rotate", false); e != nil || v.State != "bound" || v.Version != "2" {
		t.Fatal(v, e)
	}
	persisted, e := m.repo.readMarket(ctx)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(persisted)
	for _, secret := range []string{token1, token2, device, request.StartSecret} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("plaintext secret at rest")
		}
	}
	view, e := m.bindingStatus(ctx)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(view)
	if bytes.Contains(raw, []byte(token2)) {
		t.Fatal("UI secret leak")
	}
	wrong, _ := crypto.NewBox(bytes.Repeat([]byte{9}, 32))
	m.bindingBox = wrong
	if v, e := m.bindingStatus(ctx); e != nil || v.State != "recovery_required" {
		t.Fatal(v, e)
	}
	m.bindingBox = box
	now := time.Now().Unix()
	license := pl.License{SchemaVersion: 1, KeyID: "license-test", Issuer: issuer, LicenseID: "license-a", PluginID: "plugin-a", InstanceID: "instance-binding", Revision: "1", IssuedAt: now, NotBefore: now, ExpiresAt: now + 3600, Status: "active", MinVersion: "1.0.0", MaxVersionExclusive: "2.0.0"}
	first, e := pl.Sign(license, key)
	if e != nil {
		t.Fatal(e)
	}
	// Sync authenticates selected issuer, then atomically accepts the entire batch.
	second := first
	second.License.PluginID = "plugin-b"
	if e = m.acceptBindingSync(ctx, issuer, mb.Sync{Licenses: []pl.Envelope{first, second}}); e == nil {
		t.Fatal("bad batch accepted")
	}
	if len(m.licensing.state.Licenses) != 0 {
		t.Fatal("failed batch partially published")
	}
	if e = m.acceptBindingSync(ctx, issuer, mb.Sync{Licenses: []pl.Envelope{first}}); e != nil {
		t.Fatal(e)
	}
	if e = m.acceptBindingSync(ctx, "https://other.example", mb.Sync{Licenses: []pl.Envelope{first}}); e == nil {
		t.Fatal("foreign market accepted")
	}
	if _, e = act("revoke", true); e == nil {
		t.Fatal("lost revoke not reported")
	}
	if v, e := m.bindingStatus(ctx); e != nil || v.State != "revoking" {
		t.Fatal(v, e)
	}
	if e = m.repo.configureMarket(ctx, "https://other.example"); e == nil {
		t.Fatal("changed endpoint before remote revoke")
	}
	if v, e := act("revoke", true); e != nil || v.State != "unbound" {
		t.Fatal(v, e)
	}
	if len(m.licensing.state.Licenses) != 1 {
		t.Fatal("unbinding erased offline license")
	}
	if e = m.repo.configureMarket(ctx, "https://other.example"); e != nil {
		t.Fatal(e)
	}
	if _, e = m.bindingOperation(ctx, bindingAction{Action: "start"}, port.Actor{}); e == nil {
		t.Fatal("nonadmin binding")
	}
}

func TestAutomaticEntitlementAndSafetySync(t *testing.T) {
	s, _, ctx, _ := serviceFixture(t)
	m := s.manager
	box, _ := crypto.NewBox(bytes.Repeat([]byte{2}, 32))
	m.bindingBox = box
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	issuer := "https://market.example"
	m.licensing = &licensePolicy{identity: fixedIdentity("auto-instance"), issuer: issuer, roots: map[string]pl.Root{"license-test": {Issuer: issuer, PublicKey: pub}}, state: pl.NewState(), now: time.Now}
	if e := m.loadLicensing(ctx); e != nil {
		t.Fatal(e)
	}
	now := time.Now().Unix()
	license, e := pl.Sign(pl.License{SchemaVersion: 1, KeyID: "license-test", Issuer: issuer, LicenseID: "license-auto", PluginID: "plugin-auto", InstanceID: "auto-instance", Revision: "1", IssuedAt: now, NotBefore: now, ExpiresAt: now + 3600, Status: "active", MinVersion: "1.0.0", MaxVersionExclusive: "2.0.0"}, key)
	if e != nil {
		t.Fatal(e)
	}
	safety, e := pl.SignRevocations(pl.RevocationList{SchemaVersion: 1, KeyID: "license-test", Issuer: issuer, Sequence: "1", IssuedAt: now, Entries: []pl.Revocation{{PluginID: "unsafe-auto", Reason: "unsafe test"}}}, key)
	if e != nil {
		t.Fatal(e)
	}
	token, _ := mb.Random()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/safety") {
			json.NewEncoder(w).Encode(mb.Sync{Licenses: []pl.Envelope{}, Revocations: []pl.RevocationEnvelope{safety}})
			return
		}
		if r.URL.Path != mb.Prefix+"/sync" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("unexpected worker request")
		}
		json.NewEncoder(w).Encode(mb.Sync{Licenses: []pl.Envelope{license}, Revocations: []pl.RevocationEnvelope{safety}})
	}))
	defer remote.Close()
	t.Setenv("ZCARD_MARKET_TEST_ORIGIN", remote.URL)
	if e = m.repo.configureMarket(ctx, remote.URL); e != nil {
		t.Fatal(e)
	}
	v, e := m.repo.readMarket(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.saveBinding(ctx, &v, bindingContext{Credential: mb.Credential{Token: token, AccountID: "17", Version: "1", ExpiresAt: now + int64(20*24*time.Hour/time.Second)}}, "auto-instance"); e != nil {
		t.Fatal(e)
	}
	stop := m.startEntitlementSync(time.Millisecond, 10*time.Millisecond)
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, e := m.bindingStatus(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if status.LastSync > 0 && status.SafetyCheckedAt > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not persist sync")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.coordinator.mu.Lock()
	defer m.coordinator.mu.Unlock()
	if len(m.licensing.state.Licenses) != 1 || len(m.licensing.state.Revocations) != 1 {
		t.Fatal("worker did not publish authenticated state")
	}
}
