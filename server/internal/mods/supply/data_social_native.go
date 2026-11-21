package supply

// Bridge a non-idempotent provider to the retail quote/task workflow. Quotes
// and mutation markers are durable; adapter objects themselves hold no ledger.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialoperation"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialquote"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialtask"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type nativeSocialClient struct {
	g        *Gateway
	conn     *ent.SupplyConnection
	provider supplyport.SocialProvider
}

func nativeSocialRevision(c *ent.SupplyConnection) string {
	b, _ := json.Marshal([]any{data.SocialConnectionIdentity(c), c.ExchangeRate, c.Settings})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func (n *nativeSocialClient) schema(ctx context.Context, id string) (*supplyport.SocialSchema, error) {
	rows, e := n.provider.SocialServices(ctx)
	if e != nil {
		return nil, e
	}
	for _, row := range rows {
		if row.ID == id && row.IsActive {
			s := row.Schema
			return &s, nil
		}
	}
	return nil, fmt.Errorf("服务不存在或已停售")
}
func (n *nativeSocialClient) publicSchema(s supplyport.SocialSchema) supplyport.SocialSchema {
	s.Revision = fmt.Sprintf("%x", sha256.Sum256([]byte(s.Revision+nativeSocialRevision(n.conn))))
	s.Billing = supplyport.SocialBilling{Mode: "quoted_total", Currency: "CNY", Verified: s.Billing.Verified}
	return s
}
func (n *nativeSocialClient) SocialSchema(ctx context.Context, id string) (*supplyport.SocialSchema, error) {
	s, e := n.schema(ctx, id)
	if e != nil {
		return nil, e
	}
	p := n.publicSchema(*s)
	return &p, nil
}
func nativeSocialCost(s supplyport.SocialSchema, params supplyport.SocialParameters, rate float64) (int64, error) {
	if comments, ok := params["comments"]; ok {
		for _, line := range strings.Split(comments, "\n") {
			if strings.TrimSpace(line) == "" {
				return 0, fmt.Errorf("评论内容请勿包含空行")
			}
		}
	}
	exact, e := supplyport.SocialExactAmount(s, params)
	if e != nil {
		return 0, e
	}
	d, e := decimal.NewFromString(exact)
	if e != nil {
		return 0, e
	}
	// Convert the entire exact amount once, never a rounded per-unit price.
	cost := d.Mul(decimal.NewFromFloat(rate)).Shift(2).Ceil()
	if !cost.IsPositive() || cost.GreaterThan(decimal.NewFromInt(money.MaxCents)) {
		return 0, fmt.Errorf("服务总价超出支持范围")
	}
	return cost.IntPart(), nil
}
func (n *nativeSocialClient) QuoteSocial(ctx context.Context, id string, params supplyport.SocialParameters, revision string) (*supplyport.SocialQuote, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil || claims.Subject == 0 || n.g.repo.box == nil {
		return nil, fmt.Errorf("服务报价须登录")
	}
	s, e := n.schema(ctx, id)
	if e != nil {
		return nil, e
	}
	public := n.publicSchema(*s)
	if public.Revision != revision {
		return nil, fmt.Errorf("服务规则或定价已变化，请重新读取")
	}
	values, quantity, e := supplyport.ValidateSocialParameters(*s, params)
	if e != nil {
		return nil, e
	}
	cost, e := nativeSocialCost(*s, values, n.conn.ExchangeRate)
	if e != nil {
		return nil, e
	}
	qid := uuid.NewString()
	raw, _ := json.Marshal(values)
	sealed, e := n.g.repo.box.Seal(raw, []byte("native-social-quote:"+qid))
	if e != nil {
		return nil, e
	}
	schemaJSON, _ := json.Marshal(s)
	expires := time.Now().Add(5 * time.Minute).Unix()
	e = data.Client(ctx, n.g.repo.data).SocialQuote.Create().SetID(qid).SetRole("provider").SetUserID(claims.Subject).SetProductID(0).SetConnectionID(n.conn.ID).SetConnectionIdentity(data.SocialConnectionIdentity(n.conn)).SetConnectionRevision(nativeSocialRevision(n.conn)).SetSchemaRevision(revision).SetUpstreamProductID(id).SetParamsCipher(sealed).SetSchemaJSON(string(schemaJSON)).SetCurrency("CNY").SetAmountCents(cost).SetCostCents(cost).SetExpiresAt(expires).Exec(ctx)
	if e != nil {
		return nil, e
	}
	return &supplyport.SocialQuote{QuoteID: qid, ProductID: id, SchemaRevision: revision, Parameters: values, Quantity: quantity, AmountCents: supplyport.Integer(cost), Currency: "CNY", ExpiresAt: supplyport.Integer(expires)}, nil
}
func (n *nativeSocialClient) frozen(ctx context.Context, qid string) (*ent.SocialQuote, *supplyport.SocialSchema, supplyport.SocialParameters, error) {
	q, e := data.Client(ctx, n.g.repo.data).SocialQuote.Query().Where(socialquote.ID(qid), socialquote.Role("provider"), socialquote.ConnectionID(n.conn.ID)).Only(ctx)
	if e != nil {
		return nil, nil, nil, e
	}
	if q.ConnectionIdentity != data.SocialConnectionIdentity(n.conn) || n.g.repo.box == nil {
		return nil, nil, nil, fmt.Errorf("服务账号已变化")
	}
	raw, e := n.g.repo.box.Open(q.ParamsCipher, []byte("native-social-quote:"+q.ID))
	if e != nil {
		return nil, nil, nil, e
	}
	var params supplyport.SocialParameters
	var schema supplyport.SocialSchema
	if json.Unmarshal(raw, &params) != nil || json.Unmarshal([]byte(q.SchemaJSON), &schema) != nil {
		return nil, nil, nil, fmt.Errorf("服务报价快照无效")
	}
	return q, &schema, params, nil
}
func (n *nativeSocialClient) task(ctx context.Context, request string) (*ent.SocialTask, error) {
	return data.Client(ctx, n.g.repo.data).SocialTask.Query().Where(socialtask.Role("retail"), socialtask.ConnectionID(n.conn.ID), socialtask.RequestNo(request)).Only(ctx)
}
func nativeSocialRejected(task *ent.SocialTask, code string) *supplyport.SocialOrder {
	return &supplyport.SocialOrder{SupplyOrderID: "local-rejected-" + task.ID, DownstreamOrderNo: task.RequestNo, Status: "rejected", ErrorCode: code}
}
func (n *nativeSocialClient) CreateSocial(ctx context.Context, req supplyport.SocialPurchase) (*supplyport.SocialOrder, error) {
	task, e := n.task(ctx, req.DownstreamOrderNo)
	if e != nil {
		return nil, e
	}
	if task.SubmitStarted {
		return nil, supplyport.ErrSocialSubmitUnknown
	}
	q, s, params, e := n.frozen(ctx, req.QuoteID)
	if e != nil {
		return nil, e
	}
	if task.UserID != q.UserID || task.ConnectionIdentity != q.ConnectionIdentity || req.RequiredCapability != supplyport.SocialCapability || req.Currency != "CNY" || req.ProductID != q.UpstreamProductID || req.SchemaRevision != q.SchemaRevision || !maps.Equal(params, req.Parameters) || int64(req.MaxSupplyAmountCents) != q.CostCents || task.CostCents != q.CostCents {
		return nil, fmt.Errorf("服务采购与原报价不符")
	}
	current, e := n.schema(ctx, req.ProductID)
	if e != nil {
		return nil, e
	}
	if current.Revision != s.Revision || nativeSocialRevision(n.conn) != q.ConnectionRevision {
		return nativeSocialRejected(task, "quote_changed_before_submit"), nil
	}
	// Bind the quote and mark the one allowed submission BEFORE crossing HTTP.
	e = data.Tx(ctx, n.g.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, n.g.repo.data)
		count, e := c.SocialQuote.Update().Where(socialquote.ID(q.ID), socialquote.Or(socialquote.ConsumedBy(""), socialquote.ConsumedBy(req.DownstreamOrderNo))).SetConsumedBy(req.DownstreamOrderNo).Save(ctx)
		if e != nil {
			return e
		}
		if count != 1 {
			return fmt.Errorf("服务报价已使用")
		}
		count, e = c.SocialTask.Update().Where(socialtask.ID(task.ID), socialtask.SubmitStarted(false), socialtask.UpstreamOrderID(""), socialtask.LeaseToken(task.LeaseToken), socialtask.LeaseUntilGT(time.Now().Unix())).SetSubmitStarted(true).SetSubmitStartedAt(time.Now().Unix()).Save(ctx)
		if e != nil {
			return e
		}
		if count != 1 {
			return supplyport.ErrSocialSubmitUnknown
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	result, e := n.provider.CreateProviderSocial(ctx, supplyport.SocialProviderPurchase{ProductID: req.ProductID, SchemaRevision: s.Revision, Schema: *s, Parameters: params})
	if e != nil {
		var rejected *supplyport.SocialRejected
		if errors.As(e, &rejected) {
			return nativeSocialRejected(task, rejected.Code), nil
		}
		return nil, supplyport.ErrSocialSubmitUnknown
	}
	if result == nil || result.UpstreamOrderID == "" {
		return nil, supplyport.ErrSocialSubmitUnknown
	}
	return &supplyport.SocialOrder{SupplyOrderID: result.UpstreamOrderID, DownstreamOrderNo: req.DownstreamOrderNo, Status: "pending", Amount: req.MaxSupplyAmountCents, Charged: true}, nil
}
func (n *nativeSocialClient) quoteForTask(ctx context.Context, task *ent.SocialTask) (*supplyport.SocialSchema, supplyport.SocialParameters, error) {
	_, schema, params, e := n.frozen(ctx, task.QuoteID)
	return schema, params, e
}
func (n *nativeSocialClient) QuerySocial(ctx context.Context, ids []string) ([]supplyport.SocialOrder, error) {
	if len(ids) == 0 || len(ids) > 50 {
		return nil, fmt.Errorf("服务查询数量无效")
	}
	out := make([]supplyport.SocialOrder, 0, len(ids))
	for _, id := range ids {
		task, e := data.Client(ctx, n.g.repo.data).SocialTask.Query().Where(socialtask.Role("retail"), socialtask.ConnectionID(n.conn.ID), socialtask.UpstreamOrderID(id)).Only(ctx)
		if e != nil {
			return nil, e
		}
		s, params, e := n.quoteForTask(ctx, task)
		if e != nil {
			return nil, e
		}
		result, e := n.provider.QueryProviderSocial(ctx, id)
		if e != nil {
			return nil, e
		}
		if result == nil || result.UpstreamOrderID != id {
			return nil, fmt.Errorf("上游订单响应不符")
		}
		_, quantity, e := supplyport.ValidateSocialParameters(*s, params)
		if e != nil {
			return nil, e
		}
		snap := result.Snapshot
		snap.Quantity = quantity
		snap.Version = supplyport.Integer(task.UpstreamVersion + 1)
		snap.Currency = "CNY"
		snap.PaidAmountCents = supplyport.Integer(task.ChargedAmount)
		snap.RefundedAmountCents = 0
		snap.RefundReference = ""
		snap.SettlementState = "paid"
		if result.Currency != s.Billing.Currency || snap.State == "partial" || snap.State == "canceled" || snap.State == "failed" || snap.State == "review" {
			snap.SettlementState = "review"
		}
		exact, err := supplyport.SocialExactAmount(*s, params)
		observed, parseErr := decimal.NewFromString(result.Charge)
		expected, expectedErr := decimal.NewFromString(exact)
		if err != nil || parseErr != nil || expectedErr != nil || !observed.Equal(expected) {
			snap.SettlementState = "review"
		}
		snap.CanCancel = s.Cancel && snap.CanCancel
		snap.CanRefill = s.Refill && snap.CanRefill
		out = append(out, supplyport.SocialOrder{SupplyOrderID: id, DownstreamOrderNo: task.RequestNo, Status: snap.State, Amount: supplyport.Integer(task.ChargedAmount), Charged: true, Snapshot: &snap})
	}
	return out, nil
}
func (n *nativeSocialClient) operation(ctx context.Context, id, operationID string) (*ent.SocialTask, *ent.SocialOperation, error) {
	task, e := data.Client(ctx, n.g.repo.data).SocialTask.Query().Where(socialtask.Role("retail"), socialtask.ConnectionID(n.conn.ID), socialtask.UpstreamOrderID(id)).Only(ctx)
	if e != nil {
		return nil, nil, e
	}
	op, e := data.Client(ctx, n.g.repo.data).SocialOperation.Query().Where(socialoperation.TaskID(task.ID), socialoperation.OperationID(operationID)).Only(ctx)
	return task, op, e
}
func nativeOperation(localID, upstreamID, status, code string) *supplyport.SocialOperation {
	return &supplyport.SocialOperation{OperationID: localID, ProviderOperationID: &upstreamID, Status: status, ErrorCode: code}
}
func (n *nativeSocialClient) ActSocial(ctx context.Context, id, action, operationID string) (*supplyport.SocialOperation, error) {
	task, op, e := n.operation(ctx, id, operationID)
	if e != nil {
		return nil, e
	}
	if op.Action != action {
		return nil, fmt.Errorf("服务操作不符")
	}
	if op.SubmitStarted {
		return nativeOperation(operationID, "", "unknown", "submit_unknown"), nil
	}
	s, _, e := n.quoteForTask(ctx, task)
	if e != nil {
		return nil, e
	}
	if (action == "cancel" && !s.Cancel) || (action == "refill" && !s.Refill) || (action != "cancel" && action != "refill") {
		return nativeOperation(operationID, "", "rejected", "unsupported_action"), nil
	}
	if task.LeaseUntil <= time.Now().Unix() {
		return nil, fmt.Errorf("服务处理租约已失效")
	}
	count, e := data.Client(ctx, n.g.repo.data).SocialOperation.Update().Where(socialoperation.ID(op.ID), socialoperation.SubmitStarted(false)).SetSubmitStarted(true).Save(ctx)
	if e != nil {
		return nil, e
	}
	if count != 1 {
		return nativeOperation(operationID, "", "unknown", "submit_unknown"), nil
	}
	result, e := n.provider.ActProviderSocial(ctx, id, action, operationID)
	if e != nil {
		var rejected *supplyport.SocialRejected
		if errors.As(e, &rejected) {
			return nativeOperation(operationID, "", "rejected", rejected.Code), nil
		}
		return nativeOperation(operationID, "", "unknown", "submit_unknown"), nil
	}
	if result == nil || result.OperationID == "" {
		return nativeOperation(operationID, "", "unknown", "submit_unknown"), nil
	}
	return nativeOperation(operationID, result.OperationID, result.Status, result.ErrorCode), nil
}
func (n *nativeSocialClient) QuerySocialOperation(ctx context.Context, id, operationID string) (*supplyport.SocialOperation, error) {
	_, op, e := n.operation(ctx, id, operationID)
	if e != nil {
		return nil, e
	}
	if op.UpstreamOperationID == "" {
		return nativeOperation(operationID, "", "unknown", "submit_unknown"), nil
	}
	result, e := n.provider.QueryProviderSocialOperation(ctx, op.UpstreamOperationID, op.Action)
	if e != nil {
		return nil, e
	}
	if result == nil {
		return nil, fmt.Errorf("服务操作响应无效")
	}
	return nativeOperation(operationID, op.UpstreamOperationID, result.Status, result.ErrorCode), nil
}

var _ supplyport.SocialClient = (*nativeSocialClient)(nil)
