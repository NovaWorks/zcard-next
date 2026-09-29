package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	ma "github.com/NovaWorks/zcard-next/server/internal/platform/marketaccount"
	kh "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/golang-jwt/jwt/v5"
)

func TestAccountViewIsolationRecoveryAndLogout(t *testing.T) {
	s, d, ctx, _ := serviceFixture(t)
	m := s.manager
	m.bindingBox, _ = crypto.NewBox(bytes.Repeat([]byte{7}, 32))
	m.licensing = &licensePolicy{instanceID: "instance-browser-1", ready: true}
	u := d.Client.AdminUser.Create().SetUsername("c3admin").SetRoleID(1).SetPasswordHash("unused").SetEnabled(true).SaveX(ctx)
	sess := d.Client.Session.Create().SetUserID(u.ID).SetRealm(session.RealmAdmin).SetAuthVersion(u.AuthVersion).SetRefreshTokenHash(strings.Repeat("e", 64)).SetExpiresAt(time.Now().Add(time.Hour)).SaveX(ctx)
	claims := &authn.Claims{Subject: u.ID, SessionID: sess.ID, AuthVersion: u.AuthVersion, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	var confirmCalls atomic.Int32
	var revokeOffline atomic.Bool
	var revoked atomic.Int32
	var started ma.ViewStart
	token := strings.Repeat("f", 64)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var out any
		switch r.URL.Path {
		case ma.StorefrontPrefix + "/capabilities":
			out = ma.Capabilities{APIVersion: "1", AccountView: true, PublicProducts: true, CustomerAccounts: true}
		case ma.ViewPrefix + "/start":
			json.NewDecoder(r.Body).Decode(&started)
			out = ma.ViewPair{PairID: started.RequestID, DeviceSecret: strings.Repeat("a", 64), UserCode: "123456abcdef", ExpiresAt: time.Now().Add(9 * time.Minute).Unix(), PollSeconds: 5}
		case ma.ViewPrefix + "/confirm":
			if confirmCalls.Add(1) == 1 {
				http.Error(w, "lost response", 503)
				return
			}
			out = ma.ViewCredential{Token: token, AccountID: "12", InstanceID: started.InstanceID, ContextID: started.ContextID, Scopes: []string{"profile:read", "orders:read", "entitlements:read", "sites:read"}, ExpiresAt: time.Now().Add(14 * time.Minute).Unix()}
		case ma.CustomerPrefix + "/me":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("incorrect view token")
			}
			out = ma.Profile{Customer: ma.Customer{AccountID: "12", DisplayName: "buyer", Status: "active", EmailMasked: "a***@example.com", EmailVerified: true}, ExpiresAt: time.Now().Add(14 * time.Minute).Unix()}
		case ma.CustomerPrefix + "/wallet":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("wallet view token")
			}
			out = ma.Wallet{Balance: "7900", Currency: "CNY"}
		case ma.ViewPrefix + "/revoke":
			if revokeOffline.Load() {
				http.Error(w, "offline", 503)
				return
			}
			revoked.Add(1)
			out = ma.Accepted{Accepted: true}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer remote.Close()
	t.Setenv("ZCARD_MARKET_TEST_ORIGIN", remote.URL)
	if e := m.repo.configureMarket(ctx, remote.URL); e != nil {
		t.Fatal(e)
	}
	begin, e := m.startAccountView(ctx, claims)
	if e != nil {
		t.Fatal(e)
	}
	proof := ma.HostContext{ContextID: begin.ContextID, ContextSecret: begin.ContextSecret}
	bad := proof
	bad.ContextSecret = strings.Repeat("0", 64)
	if _, e = m.accountViewCall(ctx, claims, bad, "me", "", ma.PageQuery{}); e == nil {
		t.Fatal("wrong browser proof accepted")
	}
	other := *claims
	other.Subject++
	if _, e = m.accountViewCall(ctx, &other, proof, "me", "", ma.PageQuery{}); e == nil {
		t.Fatal("cross admin accepted")
	}
	other = *claims
	other.SessionID++
	if _, e = m.accountViewCall(ctx, &other, proof, "me", "", ma.PageQuery{}); e == nil {
		t.Fatal("cross session accepted")
	}
	if _, e = m.accountViewCall(ctx, claims, proof, "confirm", "12", ma.PageQuery{}); e == nil {
		t.Fatal("lost response should be unavailable")
	}
	// A new manager represents process recovery with the same encrypted storage/key.
	next := NewManager(m.repo, m.packages, nil)
	next.bindingBox = m.bindingBox
	next.licensing = m.licensing
	m = next
	if _, e = m.accountViewCall(ctx, claims, proof, "confirm", "13", ma.PageQuery{}); e == nil {
		t.Fatal("confirmation account replaced")
	}
	out, e := m.accountViewCall(ctx, claims, proof, "confirm", "12", ma.PageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(out)
	if bytes.Contains(raw, []byte(token)) {
		t.Fatal("grant token leaked")
	}
	row := d.Client.Setting.Query().Where(setting.Group("plugin_market"), setting.Key("view."+proof.ContextID)).OnlyX(ctx)
	for _, secret := range []string{token, proof.ContextSecret, strings.Repeat("a", 64)} {
		if bytes.Contains(row.Value, []byte(secret)) {
			t.Fatal("plaintext secret persisted")
		}
	}
	wallet, err := m.accountViewCall(ctx, claims, proof, "wallet", "", ma.PageQuery{})
	if err != nil || wallet.(*ma.Wallet).Balance != "7900" {
		t.Fatal(wallet, err)
	}
	if _, err = m.accountViewCall(ctx, &other, proof, "wallet", "", ma.PageQuery{}); err == nil {
		t.Fatal("cross session wallet")
	}
	revokeOffline.Store(true)
	if _, e = m.accountViewCall(ctx, claims, proof, "logout", "", ma.PageQuery{}); e != nil {
		t.Fatal(e)
	}
	if _, e = m.accountViewCall(ctx, claims, proof, "me", "", ma.PageQuery{}); e == nil {
		t.Fatal("logout cache readable")
	}
	if _, e = m.accountViewCall(ctx, claims, proof, "wallet", "", ma.PageQuery{}); e == nil {
		t.Fatal("wallet after logout")
	}
	if !d.Client.Setting.Query().Where(setting.Key("view." + proof.ContextID)).ExistX(ctx) {
		t.Fatal("offline revoke record lost")
	}
	revokeOffline.Store(false)
	m.sweepAccountViews(ctx)
	if revoked.Load() != 1 || d.Client.Setting.Query().Where(setting.Key("view."+proof.ContextID)).ExistX(ctx) {
		t.Fatal("revoke not retried")
	}
	// Switching away and back must not resurrect a proof, even before worker runs.
	begin, e = m.startAccountView(ctx, claims)
	if e != nil {
		t.Fatal(e)
	}
	proof = ma.HostContext{ContextID: begin.ContextID, ContextSecret: begin.ContextSecret}
	if e = m.repo.configureMarket(ctx, "https://another.example"); e != nil {
		t.Fatal(e)
	}
	if e = m.repo.configureMarket(ctx, remote.URL); e != nil {
		t.Fatal(e)
	}
	if _, e = m.accountViewCall(ctx, claims, proof, "confirm", "12", ma.PageQuery{}); e == nil {
		t.Fatal("origin switch resurrected context")
	}
	begin, e = m.startAccountView(ctx, claims)
	if e != nil {
		t.Fatal(e)
	}
	proof = ma.HostContext{ContextID: begin.ContextID, ContextSecret: begin.ContextSecret}
	d.Client.Session.UpdateOneID(sess.ID).SetRevokedAt(time.Now()).ExecX(ctx)
	if _, e = m.accountViewCall(ctx, claims, proof, "me", "", ma.PageQuery{}); e == nil {
		t.Fatal("local logout ignored")
	}
}
func TestSiteChallengeRootAndExpiry(t *testing.T) {
	s, _, ctx, _ := serviceFixture(t)
	s.manager.licensing = &licensePolicy{instanceID: "instance-challenge", ready: true}
	srv := kh.NewServer()
	s.RegisterMarketAccount(srv)
	v := ma.Verification{Origin: "https://site.example", ExpiresAt: time.Now().Add(time.Minute).Unix(), Challenge: ma.SiteChallenge{SchemaVersion: 1, ChallengeID: strings.Repeat("b", 32), InstanceID: "instance-challenge", Proof: strings.Repeat("c", 64)}}
	if e := s.manager.repo.putMarketSetting(ctx, "site_challenge", v); e != nil {
		t.Fatal(e)
	}
	request := func(host string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "https://"+host+ma.ChallengePath, nil)
		srv.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable challenge")
		}
		return w.Code
	}
	if request("site.example") != 200 || request("other.example") != 404 {
		t.Fatal("root host scope")
	}
	v.ExpiresAt = time.Now().Unix() - 1
	s.manager.repo.putMarketSetting(ctx, "site_challenge", v)
	if request("site.example") != 404 {
		t.Fatal("expired challenge served")
	}
	if _, e := s.manager.startAccountView(context.Background(), &authn.Claims{}); e == nil {
		t.Fatal("anonymous context")
	}
}
func TestHostPersistentRateLimit(t *testing.T) {
	s, _, ctx, _ := serviceFixture(t)
	for i := 0; i < 60; i++ {
		if e := s.manager.repo.accountRate(ctx, "host", "admin-1"); e != nil {
			t.Fatal(i, e)
		}
	}
	r := NewRepo(s.manager.repo.data, s.manager.coordinator, nil)
	if e := r.accountRate(ctx, "host", "admin-1"); e == nil {
		t.Fatal("restart reset rate")
	}
	if e := r.accountRate(ctx, "host", "admin-2"); e != nil {
		t.Fatal(e)
	}
}

func TestUnknownConfirmationRetainedUntilHardExpiry(t *testing.T) {
	s, d, ctx, _ := serviceFixture(t)
	m := s.manager
	m.bindingBox, _ = crypto.NewBox(bytes.Repeat([]byte{7}, 32))
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(ma.Failure{Code: "market.SESSION_EXPIRED", Message: "expired", RequestID: ""})
	}))
	defer remote.Close()
	t.Setenv("ZCARD_MARKET_TEST_ORIGIN", remote.URL)
	v := accountView{ID: strings.Repeat("1", 32), Origin: remote.URL, Admin: 1, Revoked: true, ConfirmAccount: "12", HardExpiry: time.Now().Add(time.Minute).Unix(), Pair: ma.ViewPair{PairID: strings.Repeat("2", 32), DeviceSecret: strings.Repeat("3", 64)}}
	if e := m.saveView(ctx, v); e != nil {
		t.Fatal(e)
	}
	m.revokeView(ctx, &v)
	if !d.Client.Setting.Query().Where(setting.Key("view." + v.ID)).ExistX(ctx) {
		t.Fatal("unknown grant record discarded before hard expiry")
	}
	v.HardExpiry = time.Now().Unix() - 1
	m.revokeView(ctx, &v)
	if d.Client.Setting.Query().Where(setting.Key("view." + v.ID)).ExistX(ctx) {
		t.Fatal("hard-expired record kept")
	}
}

// Fail the second bucket write so retry must roll back the first bucket too.
type rateBusyError struct{}

func (rateBusyError) Error() string { return "test SQLITE_BUSY" }
func (rateBusyError) Code() int     { return 5 }
func TestAccountRateRetriesWholeTransaction(t *testing.T) {
	s, d, ctx, _ := serviceFixture(t)
	var writes atomic.Int32
	d.Client.Setting.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if writes.Add(1) == 2 {
				return nil, rateBusyError{}
			}
			return next.Mutate(ctx, m)
		})
	})
	for i := 0; i < 60; i++ {
		if err := s.manager.repo.accountRate(ctx, "host", "retry-admin"); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := s.manager.repo.accountRate(ctx, "host", "retry-admin"); err == nil {
		t.Fatal("retry lost successful increments")
	}
	for _, rule := range ma.RateLimits()["host"] {
		value := "retry-admin"
		if rule.Key == "global" {
			value = "global"
		}
		var bucket struct{ Count int }
		if err := s.manager.repo.marketSetting(ctx, "rate."+viewHash("host:"+rule.Key+":"+value), &bucket); err != nil || bucket.Count != 60 {
			t.Fatal("partial transaction consumed quota", bucket, err)
		}
	}
}
