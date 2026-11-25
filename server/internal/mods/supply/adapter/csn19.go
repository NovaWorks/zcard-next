package adapter

// 19csn's documented form API is an asynchronous service API, not card stock.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/shopspring/decimal"
)

type csn19Adapter struct {
	t   *transport
	key string
}

func newCSN19(base string, credentials Credentials, retries []int) (Adapter, error) {
	if strings.TrimSpace(credentials.APIKey) == "" {
		return nil, ErrCredentialsInvalid
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/api") {
		base += "/v2"
	} else if !strings.HasSuffix(base, "/api/v2") {
		base += "/api/v2"
	}
	t, err := newTransport(base, retries, nil)
	if err != nil {
		return nil, err
	}
	return &csn19Adapter{t: t, key: credentials.APIKey}, nil
}
func (*csn19Adapter) Protocol() string { return "csn19" }

func (a *csn19Adapter) call(ctx context.Context, action string, parameters supplyport.SocialParameters, out any, mutation bool) error {
	form := url.Values{"key": {a.key}, "action": {action}}
	for key, value := range parameters {
		if key == "key" || key == "action" {
			return ErrPurchaseFormInvalid
		}
		form.Set(key, value)
	}
	// Body credentials must not be forwarded by 307/308 redirects. Mutations
	// never inherit generic retry intervals: this API has no idempotency key.
	t := *a.t
	client := *t.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t.client = &client
	raw, err := t.do(withoutRetries(ctx), "POST", "", nil, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, []byte(form.Encode()))
	if err != nil {
		if mutation {
			return supplyport.ErrSocialSubmitUnknown
		}
		return fmt.Errorf("19csn: 接口请求失败，请检查连接、密钥和渠道状态")
	}
	var failure map[string]json.RawMessage
	if json.Unmarshal(raw, &failure) == nil && len(failure["error"]) > 0 {
		var message string
		if json.Unmarshal(failure["error"], &message) == nil && strings.TrimSpace(message) != "" {
			if mutation && (len(failure["order"]) > 0 || len(failure["refill"]) > 0) {
				return supplyport.ErrSocialSubmitUnknown
			}
			return &supplyport.SocialRejected{Code: "provider_rejected"}
		}
	}
	if err := decodeZCard(raw, out); err != nil {
		if mutation {
			return supplyport.ErrSocialSubmitUnknown
		}
		return fmt.Errorf("19csn: 无效的接口响应")
	}
	return nil
}

func (a *csn19Adapter) Ping(ctx context.Context) (*PingResult, error) {
	var v struct {
		Balance  json.Number `json:"balance"`
		Currency string      `json:"currency"`
	}
	if err := a.call(ctx, "balance", nil, &v, false); err != nil {
		return nil, err
	}
	n, err := decimal.NewFromString(v.Balance.String())
	if err != nil || n.IsNegative() || !csnCurrency(v.Currency) || n.Shift(2).GreaterThan(decimal.NewFromInt(money.MaxCents)) {
		return nil, fmt.Errorf("19csn: 余额或币种响应无效")
	}
	return &PingResult{SiteName: "19csn", ProtocolVersion: "smm-v2", Capabilities: []string{supplyport.SocialCapability}, Currency: v.Currency, Balance: n.Shift(2).Floor().IntPart()}, nil
}
func csnCurrency(v string) bool {
	return len(v) == 3 && strings.Trim(v, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == ""
}
func csnInt(v int64) *int64 { return &v }
func csnID(v json.Number) bool {
	n, e := strconv.ParseUint(v.String(), 10, 64)
	return e == nil && n > 0 && strconv.FormatUint(n, 10) == v.String()
}
func csnCategory(v string) string { return fmt.Sprintf("cat_%x", sha256.Sum256([]byte(v)))[:24] }

func (a *csn19Adapter) SocialServices(ctx context.Context) ([]supplyport.SocialService, error) {
	var rows []struct {
		ID                   json.Number `json:"service"`
		Name, Type, Category string
		Description          string      `json:"description"`
		Rate                 json.Number `json:"rate"`
		Min, Max             json.Number
		Refill, Cancel       bool
	}
	if err := a.call(WithCatalogRead(ctx), "services", nil, &rows, false); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, fmt.Errorf("19csn: 无效的服务目录")
	}
	account, err := a.Ping(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]supplyport.SocialService, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		lo, e1 := strconv.ParseInt(row.Min.String(), 10, 64)
		hi, e2 := strconv.ParseInt(row.Max.String(), 10, 64)
		if !csnID(row.ID) || seen[row.ID.String()] || row.Name == "" || e1 != nil || e2 != nil || lo < 0 || hi < lo {
			return nil, fmt.Errorf("19csn: 服务编号或数量范围无效")
		}
		seen[row.ID.String()] = true
		s := supplyport.SocialSchema{Version: 1, Profile: row.Type, Quantity: supplyport.SocialQuantity{Mode: "explicit", Field: "quantity", Min: csnInt(lo), Max: csnInt(hi)}, Billing: supplyport.SocialBilling{Mode: "quantity", Rate: row.Rate.String(), RateUnit: 1000, Currency: account.Currency, Verified: true}, Cancel: row.Cancel, Refill: row.Refill, Purchasable: true}
		link := supplyport.SocialField{Key: "link", Label: "目标链接", Type: "url", Required: true, MaxLength: 4096}
		s.Fields = []supplyport.SocialField{link, {Key: "quantity", Label: "服务数量", Type: "integer", Required: true, Min: csnInt(lo), Max: csnInt(hi)}}
		switch strings.ToLower(strings.TrimSpace(row.Type)) {
		case "default": // Single run only; no unverified drip-feed multiplier.
		case "custom comments":
			s.Fields = []supplyport.SocialField{link, {Key: "comments", Label: "评论内容（每行一条，请勿留空行）", Type: "lines", Required: true, MinItems: csnInt(lo), MaxItems: csnInt(hi), MaxLength: 100000, DisallowBlankLines: true}}
			s.Quantity = supplyport.SocialQuantity{Mode: "derived_lines", LinesField: "comments", LinePolicy: "all", Min: csnInt(lo), Max: csnInt(hi)}
		case "package":
			s.Fields = []supplyport.SocialField{link}
			s.Quantity = supplyport.SocialQuantity{Mode: "absent"}
			s.Billing.Mode = "unverified"
			s.Billing.Verified = false
			s.Purchasable = false
			s.UnavailableReason = "当前服务暂不可购买，请联系商户"
		default:
			s.Purchasable = false
			s.Billing.Verified = false
			s.Fields = nil
			s.UnavailableReason = "当前服务暂不可购买，请联系商户"
		}
		if hi < 1 {
			s.Purchasable = false
			s.UnavailableReason = "当前服务暂不可购买，请联系商户"
		}
		if _, err := money.ExactServiceAmount(s.Billing.Rate, "fixed", nil, 1, 1); err != nil {
			s.Purchasable = false
			s.Billing.Verified = false
			s.UnavailableReason = "当前服务暂不可购买，请联系商户"
		}
		s.Revision = supplyport.SocialSchemaRevision(s)
		out = append(out, supplyport.SocialService{ID: row.ID.String(), Name: row.Name, Category: row.Category, Description: row.Description, IsActive: true, Schema: s})
	}
	return out, nil
}

func (a *csn19Adapter) ListProducts(ctx context.Context, page, _ int, _ bool) (*ProductList, error) {
	if page > 1 {
		return &ProductList{Items: []Product{}}, nil
	}
	services, err := a.SocialServices(ctx)
	if err != nil {
		return nil, err
	}
	out := &ProductList{Items: make([]Product, 0, len(services)), Categories: []Category{}, Total: len(services), PageSize: len(services)}
	seen := map[string]bool{}
	for _, service := range services {
		raw, _ := json.Marshal(service.Schema)
		var schema map[string]any
		_ = json.Unmarshal(raw, &schema)
		category := csnCategory(service.Category)
		out.Items = append(out.Items, Product{ID: service.ID, Name: service.Name, Description: service.Description, DescriptionSet: service.Description != "", CategoryID: category, IsActive: service.IsActive, ProductKind: supplyport.SocialProductKind, DeliveryKind: supplyport.SocialDelivery, SocialSchema: schema, Stock: -2})
		if !seen[category] {
			seen[category] = true
			out.Categories = append(out.Categories, Category{ID: category, Name: service.Category})
		}
	}
	return out, nil
}
func (a *csn19Adapter) ListCategories(ctx context.Context) ([]Category, error) {
	p, e := a.ListProducts(ctx, 1, 0, false)
	if e != nil {
		return nil, e
	}
	return p.Categories, nil
}
func (*csn19Adapter) GetStock(context.Context, string, string) (int32, error) {
	return -2, ErrNotSupported
}
func (*csn19Adapter) CreateOrder(context.Context, CreateOrderReq) (*CreateOrderResult, error) {
	return nil, ErrNotSupported
}
func (*csn19Adapter) GetOrder(context.Context, string) (*OrderDetail, error) {
	return nil, ErrNotSupported
}
func (*csn19Adapter) RefundOrder(context.Context, string) error { return ErrNotSupported }

func (a *csn19Adapter) CreateProviderSocial(ctx context.Context, r supplyport.SocialProviderPurchase) (*supplyport.SocialProviderOrder, error) {
	if r.ProductID == "" || r.SchemaRevision == "" || r.SchemaRevision != r.Schema.Revision {
		return nil, &supplyport.SocialRejected{Code: "invalid_parameters"}
	}
	params, _, err := supplyport.ValidateSocialParameters(r.Schema, r.Parameters)
	if err != nil {
		return nil, &supplyport.SocialRejected{Code: "invalid_parameters"}
	}
	if _, err = supplyport.SocialExactAmount(r.Schema, params); err != nil {
		return nil, &supplyport.SocialRejected{Code: "invalid_parameters"}
	}
	if comments, ok := params["comments"]; ok {
		for _, line := range strings.Split(comments, "\n") {
			if strings.TrimSpace(line) == "" {
				return nil, &supplyport.SocialRejected{Code: "invalid_parameters"}
			}
		}
	}
	params["service"] = r.ProductID
	var v struct {
		Order json.Number `json:"order"`
	}
	if err = a.call(ctx, "add", params, &v, true); err != nil {
		return nil, err
	}
	if !csnID(v.Order) {
		return nil, supplyport.ErrSocialSubmitUnknown
	}
	return &supplyport.SocialProviderOrder{UpstreamOrderID: v.Order.String(), Snapshot: supplyport.SocialSnapshot{State: "pending"}}, nil
}

func (a *csn19Adapter) QueryProviderSocial(ctx context.Context, id string) (*supplyport.SocialProviderOrder, error) {
	if !csnID(json.Number(id)) {
		return nil, ErrPurchaseFormInvalid
	}
	var v struct {
		Charge           json.Number `json:"charge"`
		Currency, Status string
		StartCount       json.RawMessage `json:"start_count"`
		Remains          json.RawMessage `json:"remains"`
	}
	if err := a.call(ctx, "status", supplyport.SocialParameters{"order": id}, &v, false); err != nil {
		return nil, err
	}
	d, err := decimal.NewFromString(v.Charge.String())
	if err != nil || d.IsNegative() || !csnCurrency(v.Currency) {
		return nil, fmt.Errorf("19csn: 无效的订单金额响应")
	}
	state := csnState(v.Status)
	return &supplyport.SocialProviderOrder{UpstreamOrderID: id, Charge: d.String(), Currency: v.Currency, Snapshot: supplyport.SocialSnapshot{State: state, StartCount: csnCount(v.StartCount), Remains: csnCount(v.Remains), CanCancel: state == "pending" || state == "processing", CanRefill: state == "completed"}}, nil
}
func csnCount(raw json.RawMessage) *int64 {
	v := strings.Trim(string(raw), `"`)
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 0 {
		return nil
	}
	return &n
}
func csnState(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "pending", "in queue", "queued":
		return "pending"
	case "processing", "in progress":
		return "processing"
	case "completed":
		return "completed"
	case "partial":
		return "partial"
	case "canceled", "cancelled":
		return "canceled"
	case "failed", "error":
		return "failed"
	default:
		return "review"
	}
}

func (a *csn19Adapter) ActProviderSocial(ctx context.Context, id, action, operationID string) (*supplyport.SocialOperation, error) {
	if !csnID(json.Number(id)) || operationID == "" {
		return nil, ErrPurchaseFormInvalid
	}
	switch action {
	case "refill":
		var v struct {
			Refill json.Number `json:"refill"`
		}
		if e := a.call(ctx, "refill", supplyport.SocialParameters{"order": id}, &v, true); e != nil {
			return nil, e
		}
		if !csnID(v.Refill) {
			return nil, supplyport.ErrSocialSubmitUnknown
		}
		return &supplyport.SocialOperation{OperationID: v.Refill.String(), Status: "pending"}, nil
	case "cancel":
		var rows []struct {
			Order  json.Number     `json:"order"`
			Cancel json.RawMessage `json:"cancel"`
		}
		if e := a.call(ctx, "cancel", supplyport.SocialParameters{"orders": id}, &rows, true); e != nil {
			return nil, e
		}
		if len(rows) != 1 || rows[0].Order.String() != id {
			return nil, supplyport.ErrSocialSubmitUnknown
		}
		if string(rows[0].Cancel) != "1" && string(rows[0].Cancel) != `"1"` {
			var failure struct{ Error string }
			if json.Unmarshal(rows[0].Cancel, &failure) == nil && failure.Error != "" {
				return &supplyport.SocialOperation{OperationID: operationID, Status: "rejected", ErrorCode: "provider_rejected"}, nil
			}
			return nil, supplyport.ErrSocialSubmitUnknown
		}
		return &supplyport.SocialOperation{OperationID: id, Status: "pending"}, nil
	default:
		return nil, ErrNotSupported
	}
}
func (a *csn19Adapter) QueryProviderSocialOperation(ctx context.Context, id, action string) (*supplyport.SocialOperation, error) {
	if action == "cancel" {
		v, e := a.QueryProviderSocial(ctx, id)
		if e != nil {
			return nil, e
		}
		status := "pending"
		if v.Snapshot.State == "canceled" {
			status = "succeeded"
		} else if v.Snapshot.State == "completed" || v.Snapshot.State == "partial" || v.Snapshot.State == "failed" {
			status = "rejected"
		}
		return &supplyport.SocialOperation{OperationID: id, Status: status}, nil
	}
	if action != "refill" || !csnID(json.Number(id)) {
		return nil, ErrNotSupported
	}
	var v struct{ Status string }
	if e := a.call(ctx, "refill_status", supplyport.SocialParameters{"refill": id}, &v, false); e != nil {
		return nil, e
	}
	status := "pending"
	switch strings.ToLower(v.Status) {
	case "completed":
		status = "succeeded"
	case "rejected", "canceled", "cancelled", "failed":
		status = "rejected"
	case "pending", "processing", "in progress":
	default:
		status = "unknown"
	}
	return &supplyport.SocialOperation{OperationID: id, Status: status}, nil
}

var _ Adapter = (*csn19Adapter)(nil)
var _ supplyport.SocialProvider = (*csn19Adapter)(nil)
