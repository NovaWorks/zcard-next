package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	mb "github.com/NovaWorks/zcard-next/server/internal/platform/marketbinding"
	pl "github.com/NovaWorks/zcard-next/server/internal/platform/pluginlicense"
)

// The complete recovery context is encrypted, including pre-confirmation secrets.
// marketMu serializes both network operations and durable context transitions.
type bindingContext struct {
	Start       mb.Start      `json:"start"`
	Pair        mb.Pair       `json:"pair"`
	Status      mb.PairStatus `json:"status"`
	Credential  mb.Credential `json:"credential"`
	Pending     string        `json:"pending"`
	OperationID string        `json:"operationId"`
	LastSync    int64         `json:"lastSync"`
}
type bindingView struct {
	SafetyCheckedAt int64  `json:"safetyCheckedAt,omitempty"`
	State           string `json:"state"`
	Origin          string `json:"origin"`
	InstanceID      string `json:"instanceId"`
	UserCode        string `json:"userCode,omitempty"`
	AccountID       string `json:"accountId,omitempty"`
	AccountName     string `json:"accountName,omitempty"`
	Version         string `json:"version,omitempty"`
	ExpiresAt       int64  `json:"expiresAt,omitempty"`
	LastSync        int64  `json:"lastSync,omitempty"`
}
type bindingAction struct {
	Action    string `json:"action"`
	AccountID string `json:"accountId,omitempty"`
	Confirm   bool   `json:"confirm,omitempty"`
}

func (m *Manager) bindingIdentity() (string, error) {
	m.coordinator.mu.Lock()
	defer m.coordinator.mu.Unlock()
	if m.licensing == nil || !m.licensing.ready {
		return "", fmt.Errorf("授权模块尚未初始化")
	}
	return m.licensing.instanceID, nil
}
func bindingAAD(origin, id string) []byte {
	return []byte("plugin-market-binding\n" + origin + "\n" + id)
}
func (m *Manager) openBinding(v marketState, id string) (bindingContext, error) {
	var b bindingContext
	if len(v.Binding) == 0 {
		return b, nil
	}
	if m.bindingBox == nil {
		return b, fmt.Errorf("市场凭据加密配置不可用")
	}
	raw, e := m.bindingBox.Open(v.Binding, bindingAAD(v.Origin, id))
	if e != nil {
		return b, fmt.Errorf("市场凭据无法解密，请使用原数据密钥或通过市场账户恢复")
	}
	if e = pl.Decode(raw, &b); e != nil {
		return b, fmt.Errorf("市场凭据状态损坏，请恢复备份")
	}
	return b, nil
}
func (m *Manager) saveBinding(ctx context.Context, v *marketState, b bindingContext, id string) error {
	if m.bindingBox == nil {
		return fmt.Errorf("市场凭据加密配置不可用")
	}
	raw, e := json.Marshal(b)
	if e != nil {
		return e
	}
	v.Binding, e = m.bindingBox.Seal(raw, bindingAAD(v.Origin, id))
	if e != nil {
		return e
	}
	return m.repo.writeMarket(ctx, *v)
}
func viewBinding(v marketState, b bindingContext, id string) bindingView {
	out := bindingView{State: "unbound", Origin: v.Origin, InstanceID: id, LastSync: b.LastSync, SafetyCheckedAt: v.SafetyCheckedAt}
	if b.Start.RequestID != "" {
		out.State = "starting"
	}
	if b.Pair.PairID != "" {
		out.State = "pending"
		out.UserCode = b.Pair.UserCode
		out.ExpiresAt = b.Pair.ExpiresAt
		if b.Status.Status == "approved" {
			out.State = "approved"
			out.AccountID = b.Status.AccountID
			out.AccountName = b.Status.AccountName
		}
		if b.Pair.ExpiresAt <= time.Now().Unix() {
			out.State = "pair_expired"
		}
	}
	if b.Credential.Token != "" {
		out.State = "bound"
		out.AccountID = b.Credential.AccountID
		out.AccountName = b.Credential.AccountName
		out.Version = b.Credential.Version
		out.ExpiresAt = b.Credential.ExpiresAt
		if out.ExpiresAt <= time.Now().Unix() {
			out.State = "credential_expired"
		}
	}
	if b.Pending != "" {
		out.State = b.Pending
	}
	return out
}
func (m *Manager) bindingStatus(ctx context.Context) (bindingView, error) {
	m.repo.marketMu.Lock()
	defer m.repo.marketMu.Unlock()
	v, e := m.repo.readMarket(ctx)
	if e != nil {
		return bindingView{}, e
	}
	id, e := m.bindingIdentity()
	if e != nil {
		return bindingView{}, e
	}
	b, e := m.openBinding(v, id)
	if e != nil {
		return bindingView{State: "recovery_required", Origin: v.Origin, InstanceID: id}, nil
	}
	return viewBinding(v, b, id), nil
}
func (m *Manager) bindingOperation(ctx context.Context, in bindingAction, actor port.Actor) (bindingView, error) {
	if !actor.InstanceAdmin || !actor.ScopeVerified || actor.SubsiteID != 0 || (actor.AdminID == 0 && !actor.LocalOperator) || m.coordinator.readOnly {
		return bindingView{}, fmt.Errorf("需要主站管理员权限")
	}
	m.repo.marketMu.Lock()
	defer m.repo.marketMu.Unlock()
	v, e := m.repo.readMarket(ctx)
	if e != nil {
		return bindingView{}, e
	}
	id, e := m.bindingIdentity()
	if e != nil {
		return bindingView{}, e
	}
	b, e := m.openBinding(v, id)
	// Explicit account recovery is also available after losing the data key.
	// This does not claim remote revocation; the UI requires a separate market step.
	if in.Action == "forget" && in.Confirm {
		v.Binding = nil
		if e = m.repo.writeMarket(ctx, v); e != nil {
			return bindingView{}, e
		}
		m.repo.auditRule(ctx, actor, "plugin.binding_local_reset", port.RuleKey{}, 0)
		return viewBinding(v, bindingContext{}, id), nil
	}
	if e != nil {
		return bindingView{}, e
	}
	if in.Action == "cancel" {
		if b.Credential.Token != "" || b.Pending != "" {
			return bindingView{}, fmt.Errorf("确认结果尚未恢复，不能取消，请重试或通过市场账户恢复")
		}
		v.Binding = nil
		e = m.repo.writeMarket(ctx, v)
		return viewBinding(v, bindingContext{}, id), e
	}
	client, e := mb.New(v.Origin, os.Getenv("ZCARD_MARKET_TEST_ORIGIN"))
	if e != nil {
		return bindingView{}, fmt.Errorf("请先配置有效市场地址")
	}
	defer client.Close()
	save := func() error { return m.saveBinding(ctx, &v, b, id) }
	switch in.Action {
	case "start":
		if b.Credential.Token != "" || b.Pending != "" {
			return bindingView{}, fmt.Errorf("已有绑定或待恢复操作")
		}
		if b.Pair.PairID != "" {
			return viewBinding(v, b, id), nil
		}
		if b.Start.RequestID == "" {
			request, err := mb.Random()
			if err != nil {
				return bindingView{}, err
			}
			secret, err := mb.Random()
			if err != nil {
				return bindingView{}, err
			}
			b.Start = mb.Start{RequestID: request, StartSecret: secret, InstanceID: id}
			if e = save(); e != nil {
				return bindingView{}, e
			}
		}
		var pair mb.Pair
		if e = client.Call(ctx, "start", "", b.Start, &pair); e != nil {
			return bindingView{}, e
		}
		if pair.PairID != b.Start.RequestID || !mb.ValidSecret(pair.DeviceSecret) || len(pair.UserCode) != 12 || pair.ExpiresAt <= time.Now().Unix() || pair.ExpiresAt > time.Now().Add(mb.PairLifetime+time.Minute).Unix() || pair.PollSeconds != mb.PollSeconds {
			return bindingView{}, fmt.Errorf("市场配对响应无效")
		}
		b.Pair = pair
	case "poll":
		if b.Pair.PairID == "" || b.Credential.Token != "" || b.Pending != "" {
			return bindingView{}, fmt.Errorf("没有待查询配对")
		}
		var status mb.PairStatus
		if e = client.Call(ctx, "poll", "", mb.Proof{PairID: b.Pair.PairID, DeviceSecret: b.Pair.DeviceSecret}, &status); e != nil {
			return bindingView{}, e
		}
		if len(status.AccountName) > 128 || (status.Status == "approved" && !validBindingAccount(status.AccountID)) || (status.Status != "pending" && status.Status != "approved") {
			return bindingView{}, fmt.Errorf("配对状态需要恢复")
		}
		b.Status = status
	case "confirm":
		if !in.Confirm || b.Pair.PairID == "" || b.Credential.Token != "" || b.Status.Status != "approved" || b.Status.AccountID == "0" || in.AccountID != b.Status.AccountID || (b.Pending != "" && b.Pending != "confirming") {
			return bindingView{}, fmt.Errorf("请核对并明确确认市场账户")
		}
		b.Pending = "confirming"
		if e = save(); e != nil {
			return bindingView{}, e
		}
		var credential mb.Credential
		if e = client.Call(ctx, "confirm", "", mb.Proof{PairID: b.Pair.PairID, DeviceSecret: b.Pair.DeviceSecret, AccountID: in.AccountID}, &credential); e != nil {
			return bindingView{}, e
		}
		if e = validateCredential(credential, in.AccountID, "0"); e != nil {
			return bindingView{}, e
		}
		b = bindingContext{Credential: credential}
	case "rotate":
		if b.Credential.Token == "" || (b.Pending != "" && b.Pending != "rotating") {
			return bindingView{}, fmt.Errorf("没有可轮换的凭据")
		}
		if b.Pending == "" {
			b.OperationID, e = mb.Random()
			if e != nil {
				return bindingView{}, e
			}
			b.Pending = "rotating"
			if e = save(); e != nil {
				return bindingView{}, e
			}
		}
		var credential mb.Credential
		if e = client.Call(ctx, "rotate", b.Credential.Token, mb.Operation{RequestID: b.OperationID}, &credential); e != nil {
			return bindingView{}, e
		}
		if e = validateCredential(credential, b.Credential.AccountID, b.Credential.Version); e != nil {
			return bindingView{}, e
		}
		b.Credential = credential
		b.Pending = ""
		b.OperationID = ""
	case "revoke":
		if !in.Confirm || b.Credential.Token == "" || (b.Pending != "" && b.Pending != "revoking") {
			return bindingView{}, fmt.Errorf("请确认解绑，或先恢复凭据轮换")
		}
		b.Pending = "revoking"
		if e = save(); e != nil {
			return bindingView{}, e
		}
		var out struct {
			Revoked bool `json:"revoked"`
		}
		if e = client.Call(ctx, "revoke", b.Credential.Token, mb.Operation{}, &out); e != nil {
			return bindingView{}, e
		}
		if !out.Revoked {
			return bindingView{}, fmt.Errorf("市场尚未确认解绑")
		}
		v.Binding = nil
		if e = m.repo.writeMarket(ctx, v); e != nil {
			return bindingView{}, e
		}
		m.repo.auditRule(ctx, actor, "plugin.binding_revoke", port.RuleKey{}, 0)
		return viewBinding(v, bindingContext{}, id), nil
	case "sync":
		if b.Credential.Token == "" || b.Pending != "" {
			return bindingView{}, fmt.Errorf("请先完成绑定或恢复待处理操作")
		}
		var out mb.Sync
		if e = client.Call(ctx, "sync", b.Credential.Token, struct{}{}, &out); e != nil {
			return bindingView{}, e
		}
		if e = m.acceptBindingSync(ctx, v.Origin, out); e != nil {
			return bindingView{}, e
		}
		b.LastSync = time.Now().Unix()
		v.SafetyCheckedAt = b.LastSync
	default:
		return bindingView{}, fmt.Errorf("未知市场绑定操作")
	}
	if e = save(); e != nil {
		return bindingView{}, e
	}
	if in.Action != "poll" && in.Action != "sync" {
		m.repo.auditRule(ctx, actor, "plugin.binding_"+in.Action, port.RuleKey{}, 0)
	}
	return viewBinding(v, b, id), nil
}
func validBindingAccount(id string) bool {
	n, e := strconv.ParseUint(id, 10, 64)
	return e == nil && n > 0 && strconv.FormatUint(n, 10) == id
}
func validateCredential(c mb.Credential, account, previous string) error {
	n, e := pl.Revision(c.Version)
	old, _ := strconv.ParseUint(previous, 10, 64)
	if e != nil || n <= old || !validBindingAccount(c.AccountID) || len(c.AccountName) > 128 || !mb.ValidSecret(c.Token) || c.AccountID != account || c.ExpiresAt <= time.Now().Unix() || c.ExpiresAt > time.Now().Add(mb.CredentialLifetime+time.Minute).Unix() {
		return fmt.Errorf("市场凭据响应无效")
	}
	return nil
}
func (m *Manager) acceptBindingSync(ctx context.Context, origin string, in mb.Sync) error {
	m.coordinator.mu.Lock()
	defer m.coordinator.mu.Unlock()
	p := m.licensing
	if p == nil || !p.ready || (p.issuer != origin && origin != os.Getenv("ZCARD_MARKET_TEST_ORIGIN")) || len(in.Licenses) > pl.MaxLicenses || len(in.Revocations) > 1 {
		return fmt.Errorf("市场与受信授权签发者不匹配")
	}
	next := p.state
	for _, env := range in.Licenses {
		if env.License.Issuer != p.issuer {
			return pl.ErrInvalid
		}
		var e error
		next, e = next.Accept(env, p.roots, p.instanceID, p.domain, p.now())
		if e != nil {
			return e
		}
	}
	for _, env := range in.Revocations {
		if env.List.Issuer != p.issuer {
			return pl.ErrInvalid
		}
		var e error
		next, e = next.AcceptRevocations(env, p.roots, p.now())
		if e != nil {
			return e
		}
	}
	raw, e := json.Marshal(next)
	if e != nil || len(raw) > 2<<20 {
		return pl.ErrInvalid
	}
	if e = data.Client(ctx, m.repo.data).Setting.Create().SetGroup("plugin_entitlements").SetKey("state").SetValue(raw).OnConflict(sql.ConflictColumns(setting.FieldGroup, setting.FieldKey)).UpdateValue().Exec(ctx); e != nil {
		return e
	}
	p.state = next
	return nil
}

// Background synchronization never blocks runtime acquisition or free plugins.
// Caller must stop this worker before closing the database.
func (m *Manager) StartEntitlementSync() func() {
	return m.startEntitlementSync(5*time.Second, 5*time.Minute)
}
func (m *Manager) startEntitlementSync(initial, interval time.Duration) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(initial)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				child, stop := context.WithTimeout(ctx, 65*time.Second)
				m.sweepAccountViews(child)
				_ = m.syncMarketSafety(child)
				view, e := m.bindingStatus(child)
				if e == nil && (view.State == "rotating" || (view.State == "bound" && view.ExpiresAt <= time.Now().Add(7*24*time.Hour).Unix())) {
					_, e = m.bindingOperation(child, bindingAction{Action: "rotate"}, LocalActor())
				}
				if e == nil && (view.State == "bound" || view.State == "rotating") {
					_, _ = m.bindingOperation(child, bindingAction{Action: "sync"}, LocalActor())
				}
				stop()
				if view.State == "rotating" {
					timer.Reset(min(30*time.Second, interval))
				} else {
					timer.Reset(interval)
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}

// Return credentials only for the exact configured origin and no uncertain transition.
func (m *Manager) marketCredential(ctx context.Context, origin string) (string, error) {
	m.repo.marketMu.Lock()
	defer m.repo.marketMu.Unlock()
	v, e := m.repo.readMarket(ctx)
	if e != nil {
		return "", e
	}
	if v.Origin != origin {
		return "", fmt.Errorf("market changed")
	}
	if len(v.Binding) == 0 {
		return "", nil
	}
	id, e := m.bindingIdentity()
	if e != nil {
		return "", e
	}
	b, e := m.openBinding(v, id)
	if e != nil {
		return "", e
	}
	if b.Credential.Token == "" {
		return "", nil
	}
	if b.Pending != "" || b.Credential.ExpiresAt <= time.Now().Unix() {
		return "", fmt.Errorf("请先恢复市场凭据")
	}
	return b.Credential.Token, nil
}

// Safety policy is fetched independently of account binding so free plugins and
// an unbound instance can still receive emergency safety decisions.
func (m *Manager) syncMarketSafety(ctx context.Context) error {
	m.repo.marketMu.Lock()
	defer m.repo.marketMu.Unlock()
	v, e := m.repo.readMarket(ctx)
	if e != nil {
		return e
	}
	if v.Origin == "" {
		return nil
	}
	m.coordinator.mu.Lock()
	configured := m.licensing != nil && m.licensing.ready && m.licensing.issuer != ""
	m.coordinator.mu.Unlock()
	if !configured {
		return nil
	}
	client, e := mb.New(v.Origin, os.Getenv("ZCARD_MARKET_TEST_ORIGIN"))
	if e != nil {
		return e
	}
	defer client.Close()
	var out mb.Sync
	if e = client.Call(ctx, "safety", "", struct{}{}, &out); e != nil {
		return e
	}
	if len(out.Licenses) != 0 {
		return pl.ErrInvalid
	}
	if e = m.acceptBindingSync(ctx, v.Origin, out); e != nil {
		return e
	}
	v.SafetyCheckedAt = time.Now().Unix()
	return m.repo.writeMarket(ctx, v)
}
