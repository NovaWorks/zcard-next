package plugin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pluginoperation"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	ma "github.com/NovaWorks/zcard-next/server/internal/platform/marketaccount"
)

var viewExpired = &ma.RemoteError{Status: 401, Code: "market.REAUTH_REQUIRED"}

// One independently encrypted record per browser context, never the instance binding.
type accountView struct {
	ID, Origin     string
	Admin, Session uint64
	AuthVersion    int
	LocalExpiry    int64
	SecretHash     string
	Pair           ma.ViewPair
	ConfirmAccount string
	Credential     ma.ViewCredential
	Revoked        bool
	HardExpiry     int64
}
type sealedView struct {
	Origin string
	Admin  uint64
	Cipher []byte
}

func viewHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func viewRandom(n int) (string, error) {
	b := make([]byte, n)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func viewAAD(id, origin string, admin uint64) []byte {
	return []byte(fmt.Sprintf("market-account-view\n%s\n%s\n%d", id, origin, admin))
}
func (r *Repo) marketSetting(ctx context.Context, key string, out any) error {
	row, e := data.Client(ctx, r.data).Setting.Query().Where(setting.Group("plugin_market"), setting.Key(key)).Only(ctx)
	if e != nil {
		return e
	}
	return json.Unmarshal(row.Value, out)
}
func (r *Repo) putMarketSetting(ctx context.Context, key string, v any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return data.Client(ctx, r.data).Setting.Create().SetGroup("plugin_market").SetKey(key).SetValue(raw).OnConflict(sql.ConflictColumns(setting.FieldGroup, setting.FieldKey)).UpdateValue().Exec(ctx)
}
func (m *Manager) saveView(ctx context.Context, v accountView) error {
	if m.bindingBox == nil {
		return ma.ErrUnavailable
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	cipher, e := m.bindingBox.Seal(raw, viewAAD(v.ID, v.Origin, v.Admin))
	if e != nil {
		return e
	}
	return m.repo.putMarketSetting(ctx, "view."+v.ID, sealedView{v.Origin, v.Admin, cipher})
}
func (m *Manager) readView(ctx context.Context, id string) (accountView, error) {
	var sealed sealedView
	var v accountView
	if m.bindingBox == nil {
		return v, ma.ErrUnavailable
	}
	if e := m.repo.marketSetting(ctx, "view."+id, &sealed); e != nil {
		if ent.IsNotFound(e) {
			return v, viewExpired
		}
		return v, ma.ErrUnavailable
	}
	raw, e := m.bindingBox.Open(sealed.Cipher, viewAAD(id, sealed.Origin, sealed.Admin))
	if e != nil || json.Unmarshal(raw, &v) != nil || v.ID != id || v.Origin != sealed.Origin || v.Admin != sealed.Admin {
		return v, ma.ErrUnavailable
	}
	var revoked bool
	if err := m.repo.marketSetting(ctx, "revoked."+id, &revoked); err == nil {
		v.Revoked = v.Revoked || revoked
	} else if !ent.IsNotFound(err) {
		return v, ma.ErrUnavailable
	}
	return v, nil
}

// Access tokens issued before C3 must be refreshed/re-authenticated to identify their session.
func (m *Manager) liveViewSession(ctx context.Context, admin, sid uint64, version int, expiry int64) bool {
	if sid == 0 || expiry <= time.Now().Unix() {
		return false
	}
	s, e := data.Client(ctx, m.repo.data).Session.Query().Where(session.ID(sid), session.UserID(admin), session.RealmEQ(session.RealmAdmin), session.AuthVersion(version), session.RevokedAtIsNil(), session.ExpiresAtGT(time.Now())).Only(ctx)
	if e != nil || s == nil {
		return false
	}
	u, e := data.Client(ctx, m.repo.data).AdminUser.Get(ctx, admin)
	return e == nil && u.Enabled && u.AuthVersion == version
}
func (m *Manager) startAccountView(ctx context.Context, claims *authn.Claims) (ma.HostBegin, error) {
	m.repo.marketMu.Lock()
	defer m.repo.marketMu.Unlock()
	var out ma.HostBegin
	if claims.ExpiresAt == nil || !m.liveViewSession(ctx, claims.Subject, claims.SessionID, claims.AuthVersion, claims.ExpiresAt.Unix()) {
		return out, viewExpired
	}
	state, e := m.repo.readMarket(ctx)
	if e != nil {
		return out, e
	}
	client, e := ma.NewClient(state.Origin, os.Getenv("ZCARD_MARKET_TEST_ORIGIN"))
	if e != nil {
		return out, e
	}
	defer client.Close()
	var caps ma.Capabilities
	if e = client.Call(ctx, "capabilities", "", ma.Empty{}, &caps); e != nil {
		return out, e
	}
	if !caps.AccountView {
		return out, ma.ErrUnsupported
	}
	instance, e := m.bindingIdentity()
	if e != nil {
		return out, e
	}
	id, e := viewRandom(16)
	if e != nil {
		return out, e
	}
	secret, e := viewRandom(32)
	if e != nil {
		return out, e
	}
	startSecret, e := viewRandom(32)
	if e != nil {
		return out, e
	}
	requestID, e := viewRandom(16)
	if e != nil {
		return out, e
	}
	v := accountView{ID: id, Origin: state.Origin, Admin: claims.Subject, Session: claims.SessionID, AuthVersion: claims.AuthVersion, LocalExpiry: claims.ExpiresAt.Unix(), SecretHash: viewHash(secret), HardExpiry: time.Now().Add(ma.PairLifetime + ma.ViewLifetime).Unix()}
	// No grant exists until explicit confirmation. An abandoned start expires remotely.
	if e = client.Call(ctx, "view-start", "", ma.ViewStart{RequestID: requestID, StartSecret: startSecret, InstanceID: instance, ContextID: id}, &v.Pair); e != nil {
		return out, e
	}
	if v.Pair.ExpiresAt > time.Now().Add(ma.PairLifetime).Unix() || v.Pair.ExpiresAt <= time.Now().Unix() {
		return out, ma.ErrUnavailable
	}
	if e = m.saveView(ctx, v); e != nil {
		return out, e
	}
	return ma.HostBegin{ContextID: id, ContextSecret: secret, UserCode: v.Pair.UserCode, ExpiresAt: v.Pair.ExpiresAt, PollSeconds: v.Pair.PollSeconds}, nil
}
func (m *Manager) accountViewCall(ctx context.Context, claims *authn.Claims, proof ma.HostContext, op, account string, page ma.PageQuery) (any, error) {
	m.repo.marketMu.Lock()
	defer m.repo.marketMu.Unlock()
	v, e := m.readView(ctx, proof.ContextID)
	if e != nil {
		return nil, e
	}
	// Never mutate another context as a side effect of a failed proof.
	if v.Admin != claims.Subject || v.Session != claims.SessionID || v.AuthVersion != claims.AuthVersion || subtle.ConstantTimeCompare([]byte(v.SecretHash), []byte(viewHash(proof.ContextSecret))) != 1 {
		return nil, viewExpired
	}
	if op == "logout" {
		v.Revoked = true
		if e = m.saveView(ctx, v); e != nil {
			return nil, e
		}
		m.revokeView(ctx, &v)
		return ma.Accepted{Accepted: true}, nil
	}
	state, e := m.repo.readMarket(ctx)
	if e != nil {
		return nil, e
	}
	if v.Revoked || v.Origin != state.Origin || v.HardExpiry <= time.Now().Unix() || !m.liveViewSession(ctx, v.Admin, v.Session, v.AuthVersion, v.LocalExpiry) {
		v.Revoked = true
		if e = m.saveView(ctx, v); e != nil {
			return nil, e
		}
		m.revokeView(ctx, &v)
		return nil, viewExpired
	}
	client, e := ma.NewClient(v.Origin, os.Getenv("ZCARD_MARKET_TEST_ORIGIN"))
	if e != nil {
		return nil, e
	}
	defer client.Close()
	var out any
	switch op {
	case "poll":
		out = &ma.ViewStatus{}
		e = client.Call(ctx, "view-poll", "", ma.ViewProof{PairID: v.Pair.PairID, DeviceSecret: v.Pair.DeviceSecret}, out)
	case "confirm":
		if v.ConfirmAccount != "" && v.ConfirmAccount != account {
			return nil, viewExpired
		}
		v.ConfirmAccount = account
		// Persist the immutable account choice before the remote call; retries recover lost responses.
		if e = m.saveView(ctx, v); e != nil {
			return nil, e
		}
		if v.Credential.Token == "" {
			e = client.Call(ctx, "view-confirm", "", ma.ViewConfirm{PairID: v.Pair.PairID, DeviceSecret: v.Pair.DeviceSecret, AccountID: account}, &v.Credential)
			if e == nil {
				instance, err := m.bindingIdentity()
				if err != nil {
					return nil, err
				}
				if v.Credential.ContextID != v.ID || v.Credential.AccountID != account || v.Credential.InstanceID != instance || v.Credential.ExpiresAt > time.Now().Add(ma.ViewLifetime).Unix() || v.Credential.ExpiresAt <= time.Now().Unix() {
					return nil, ma.ErrUnavailable
				}
				e = m.saveView(ctx, v)
			}
		}
		if e == nil {
			out = &ma.Profile{}
			e = client.Call(ctx, "me", v.Credential.Token, ma.Empty{}, out)
		}
	case "me", "orders", "entitlements", "sites", "wallet":
		if v.Credential.Token == "" || v.Credential.ExpiresAt <= time.Now().Unix() {
			return nil, viewExpired
		}
		var input any = page
		switch op {
		case "me":
			out = &ma.Profile{}
			input = ma.Empty{}
		case "wallet":
			out = &ma.Wallet{}
			input = ma.Empty{}
		case "orders":
			out = &ma.OrderPage{}
		case "entitlements":
			out = &ma.EntitlementPage{}
		case "sites":
			out = &ma.SitePage{}
		}
		e = client.Call(ctx, op, v.Credential.Token, input, out)
	default:
		return nil, ma.ErrInvalid
	}
	if ma.Terminal(e) {
		v.Revoked = true
		if saveErr := m.saveView(ctx, v); saveErr != nil {
			return nil, saveErr
		}
	}
	return out, e
}

// Revocation-only tombstones survive offline logout and origin changes. Never use current origin.
func (m *Manager) revokeView(ctx context.Context, v *accountView) {
	if v.HardExpiry <= time.Now().Unix() {
		m.deleteView(ctx, v.ID)
		return
	}
	client, e := ma.NewClient(v.Origin, os.Getenv("ZCARD_MARKET_TEST_ORIGIN"))
	if e != nil {
		return
	}
	defer client.Close()
	if v.Credential.Token == "" && v.ConfirmAccount != "" {
		e = client.Call(ctx, "view-confirm", "", ma.ViewConfirm{PairID: v.Pair.PairID, DeviceSecret: v.Pair.DeviceSecret, AccountID: v.ConfirmAccount}, &v.Credential)
		// An expired pairing proof may hide a successfully issued but lost grant.
		// Retain the tombstone until the hard expiry when recovery is impossible.
		if ma.Terminal(e) {
			return
		}
		if e != nil {
			return
		}
		if m.saveView(ctx, *v) != nil {
			return
		}
	}
	if v.Credential.Token == "" {
		m.deleteView(ctx, v.ID)
		return
	}
	var out ma.Accepted
	e = client.Call(ctx, "view-revoke", v.Credential.Token, ma.Operation{RequestID: v.ID}, &out)
	if e == nil || ma.Terminal(e) {
		m.deleteView(ctx, v.ID)
	}
}
func (m *Manager) deleteView(ctx context.Context, id string) {
	_, _ = data.Client(ctx, m.repo.data).Setting.Delete().Where(setting.Group("plugin_market"), setting.KeyIn("view."+id, "revoked."+id)).Exec(ctx)
}
func (m *Manager) sweepAccountViews(ctx context.Context) {
	m.repo.marketMu.Lock()
	defer m.repo.marketMu.Unlock()
	_, _ = data.Client(ctx, m.repo.data).Setting.Delete().Where(setting.Group("plugin_market"), setting.KeyHasPrefix("rate."), setting.UpdatedAtLT(time.Now().Add(-10*time.Minute))).Exec(ctx)
	state, e := m.repo.readMarket(ctx)
	if e != nil {
		return
	}
	rows, e := data.Client(ctx, m.repo.data).Setting.Query().Where(setting.Group("plugin_market"), setting.KeyHasPrefix("view.")).All(ctx)
	if e != nil {
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		v, e := m.readView(ctx, row.Key[5:])
		if e != nil {
			continue
		}
		if v.Origin != state.Origin || !m.liveViewSession(ctx, v.Admin, v.Session, v.AuthVersion, v.LocalExpiry) || v.HardExpiry <= time.Now().Unix() {
			v.Revoked = true
		}
		if v.Revoked {
			if m.saveView(ctx, v) == nil {
				m.revokeView(ctx, &v)
			}
		}
	}
}
func (m *Manager) applyOfficialOrigin(ctx context.Context) error {
	m.repo.marketMu.Lock()
	defer m.repo.marketMu.Unlock()
	state, e := m.repo.readMarket(ctx)
	if e != nil {
		return e
	}
	if state.Origin != "" || state.ExplicitOrigin || len(state.Binding) > 0 {
		return nil
	}
	// In-flight operations and historical market checkpoints must retain their original source.
	count, e := data.Client(ctx, m.repo.data).PluginOperation.Query().Where(pluginoperation.PhaseNotIn("reconciled", "failed")).Count(ctx)
	if e != nil {
		return e
	}
	if count > 0 || len(state.Checkpoints) > 0 || len(state.LicensedCheckpoints) > 0 {
		return nil
	}
	state.Origin = "https://store.zcard.dev"
	if m.officialProfile != nil {
		state.Origin = m.officialProfile.Origin
		state.ProfileID = m.officialProfile.ProfileID
	}
	return m.repo.writeMarket(ctx, state)
}

// Limits are persistent and serialized by the instance lock + marketMu, as are plugin operations.
func (r *Repo) accountRate(ctx context.Context, class, key string) error {
	r.marketMu.Lock()
	defer r.marketMu.Unlock()
	now := time.Now().Unix()
	consume := func() error {
		return data.Tx(ctx, r.data, func(ctx context.Context) error {
			for _, rule := range ma.RateLimits()[class] {
				value := key
				if rule.Key == "global" {
					value = "global"
				}
				name := "rate." + viewHash(class+":"+rule.Key+":"+value)
				var bucket struct {
					Window int64
					Count  int
				}
				e := r.marketSetting(ctx, name, &bucket)
				if e != nil && !ent.IsNotFound(e) {
					return e
				}
				window := now / int64(rule.WindowSeconds)
				if bucket.Window != window {
					bucket.Window = window
					bucket.Count = 0
				}
				if bucket.Count >= rule.Limit {
					return &ma.RemoteError{Status: 429, Code: "market.RATE_LIMITED"}
				}
				bucket.Count++
				if e = r.putMarketSetting(ctx, name, bucket); e != nil {
					return e
				}
			}
			return nil
		})
	}
	// SQLite cannot upgrade a read snapshot when another connection has written.
	// Replay the entire rolled-back bucket transaction, never only its final write.
	for attempt := 0; ; attempt++ {
		err := consume()
		var sqlite interface{ Code() int }
		if err == nil || attempt == 3 || !errors.As(err, &sqlite) || (sqlite.Code()&255 != 5 && sqlite.Code()&255 != 6) {
			return err
		}
		timer := time.NewTimer(time.Duration(10<<attempt) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func localAdminKey(c *authn.Claims) string { return strconv.FormatUint(c.Subject, 10) }
