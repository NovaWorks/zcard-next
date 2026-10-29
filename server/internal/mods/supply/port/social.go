package port

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

const (
	SocialCapability  = "social_service.v1"
	SocialDelivery    = "social_service"
	SocialProductKind = "social_service"
)

// SocialParameters preserves absence, explicit zero, line order and duplicate lines.
// It must not contain credentials. Display labels never identify provider fields.
type SocialParameters map[string]string

type SocialField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // text | url | username | integer | lines | date | enum
	Required    bool     `json:"required"`
	Min         *int64   `json:"min,omitempty"`
	Max         *int64   `json:"max,omitempty"`
	MaxLength   int      `json:"max_length,omitempty"`
	MinItems    *int64   `json:"min_items,omitempty"`
	MaxItems    *int64   `json:"max_items,omitempty"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
}

type SocialQuantity struct {
	Mode       string `json:"mode"` // explicit | derived_lines | absent | range_per_event
	Field      string `json:"field,omitempty"`
	LinesField string `json:"lines_field,omitempty"`
	LinePolicy string `json:"line_policy,omitempty"` // nonempty | all; required for derived_lines
	Min        *int64 `json:"min,omitempty"`
	Max        *int64 `json:"max,omitempty"`
	Step       *int64 `json:"step,omitempty"`
	StepBase   *int64 `json:"step_base,omitempty"`
	MinField   string `json:"min_field,omitempty"`
	MaxField   string `json:"max_field,omitempty"`
}

type SocialBilling struct {
	Mode                   string `json:"mode"` // quantity | fixed | per_event | unverified
	Rate                   string `json:"rate"` // exact decimal in Currency per RateUnit; private provider cost stays private
	RateUnit               int64  `json:"rate_unit"`
	Currency               string `json:"currency"`
	Verified               bool   `json:"verified"`
	RunsMultiplierVerified bool   `json:"runs_multiplier_verified"`
}

type SocialSchema struct {
	Version           int            `json:"version"`
	Revision          string         `json:"revision"`
	Profile           string         `json:"profile"`
	Fields            []SocialField  `json:"fields"`
	Quantity          SocialQuantity `json:"quantity"`
	Billing           SocialBilling  `json:"billing"`
	Cancel            bool           `json:"cancel"`
	Refill            bool           `json:"refill"`
	Purchasable       bool           `json:"purchasable"`
	UnavailableReason string         `json:"unavailable_reason,omitempty"`
}

type SocialQuote struct {
	QuoteID        string           `json:"quote_id"`
	ProductID      string           `json:"product_id"`
	SchemaRevision string           `json:"schema_revision"`
	Parameters     SocialParameters `json:"parameters"`
	Quantity       *int64           `json:"quantity,omitempty"`
	AmountCents    Integer          `json:"amount_cents"`
	Currency       string           `json:"currency"`
	ExpiresAt      Integer          `json:"expires_at"`
}

type SocialPurchase struct {
	QuoteID              string           `json:"quote_id"`
	ProductID            string           `json:"product_id"`
	SchemaRevision       string           `json:"schema_revision"`
	Parameters           SocialParameters `json:"parameters"`
	DownstreamOrderNo    string           `json:"downstream_order_no"`
	RequiredCapability   string           `json:"required_capability"`
	MaxSupplyAmountCents Integer          `json:"max_supply_amount_cents"`
	Currency             string           `json:"currency"`
}

type SocialSnapshot struct {
	State               string  `json:"state"`
	Version             Integer `json:"version"`
	Quantity            *int64  `json:"quantity,omitempty"`
	StartCount          *int64  `json:"start_count,omitempty"`
	Remains             *int64  `json:"remains,omitempty"`
	CompletedQuantity   *int64  `json:"completed_quantity,omitempty"`
	CanCancel           bool    `json:"can_cancel"`
	CanRefill           bool    `json:"can_refill"`
	SettlementState     string  `json:"settlement_state"` // paid | review | partially_refunded | refunded
	PaidAmountCents     Integer `json:"paid_amount_cents"`
	RefundedAmountCents Integer `json:"refunded_amount_cents"`
	Currency            string  `json:"currency"`
	RefundReference     string  `json:"refund_reference,omitempty"`
	ErrorCode           string  `json:"error_code,omitempty"`
}

type SocialOrder struct {
	SupplyOrderID     string          `json:"supply_order_id"`
	DownstreamOrderNo string          `json:"downstream_order_no"`
	Status            string          `json:"status"`
	Amount            Integer         `json:"amount"`
	Charged           bool            `json:"charged"`
	ErrorCode         string          `json:"error_code,omitempty"`
	Snapshot          *SocialSnapshot `json:"snapshot,omitempty"`
}

type SocialOperation struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"` // pending | succeeded | rejected | unknown
	ErrorCode   string `json:"error_code,omitempty"`
}

// SocialService is a provider catalog entry; its schema may contain private cost.
type SocialService struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Category    string       `json:"category"`
	Description string       `json:"description,omitempty"`
	IsActive    bool         `json:"is_active"`
	Schema      SocialSchema `json:"schema"`
}

type SocialProviderPurchase struct {
	ProductID      string
	SchemaRevision string
	Parameters     SocialParameters
	Schema         SocialSchema
}

type SocialProviderOrder struct {
	UpstreamOrderID string
	Snapshot        SocialSnapshot
	Charge          string // exact provider cost; never expose as the downstream price
	Currency        string
}

var ErrSocialSubmitUnknown = errors.New("social: submission outcome unknown; reconcile without resubmitting")

// SocialRejected is authoritative provider rejection before an order was accepted.
type SocialRejected struct{ Code string }

func (e *SocialRejected) Error() string { return "social: provider rejected: " + e.Code }

type SocialProvider interface {
	SocialServices(context.Context) ([]SocialService, error)
	CreateProviderSocial(context.Context, SocialProviderPurchase) (*SocialProviderOrder, error)
	QueryProviderSocial(context.Context, string) (*SocialProviderOrder, error)
	ActProviderSocial(context.Context, string, string, string) (*SocialOperation, error)
	QueryProviderSocialOperation(context.Context, string, string) (*SocialOperation, error)
}

type SocialClient interface {
	SocialSchema(context.Context, string) (*SocialSchema, error)
	QuoteSocial(context.Context, string, SocialParameters, string) (*SocialQuote, error)
	CreateSocial(context.Context, SocialPurchase) (*SocialOrder, error)
	QuerySocial(context.Context, []string) ([]SocialOrder, error)
	ActSocial(context.Context, string, string, string) (*SocialOperation, error)
	QuerySocialOperation(context.Context, string, string) (*SocialOperation, error)
}

// SocialClientGateway opens the authenticated client for the frozen connection.
type SocialClientGateway interface {
	OpenSocial(context.Context, uint64) (SocialClient, error)
}

type SocialProviderGateway interface {
	OpenSocialProvider(context.Context, uint64) (SocialProvider, error)
}

// SocialExactAmount retains all provider precision until the caller applies FX
// and its own price rules. A subscription has no finite quote without a budget.
func SocialExactAmount(s SocialSchema, parameters SocialParameters) (string, error) {
	if !s.Purchasable || !s.Billing.Verified || s.Billing.Currency == "" {
		return "", fmt.Errorf("service billing has not been verified")
	}
	_, quantity, err := ValidateSocialParameters(s, parameters)
	if err != nil {
		return "", err
	}
	runs := int64(1)
	if value, exists := parameters["runs"]; exists {
		var err error
		runs, err = strconv.ParseInt(value, 10, 64)
		if err != nil || runs < 1 {
			return "", fmt.Errorf("invalid service runs")
		}
		if runs > 1 {
			if !s.Billing.RunsMultiplierVerified {
				return "", fmt.Errorf("scheduled service billing has not been verified")
			}
		}
	}
	return money.ExactServiceAmount(s.Billing.Rate, s.Billing.Mode, quantity, s.Billing.RateUnit, runs)
}

func SocialAmountCents(s SocialSchema, parameters SocialParameters) (int64, error) {
	exact, err := SocialExactAmount(s, parameters)
	if err != nil {
		return 0, err
	}
	return money.ServiceAmountCents(exact)
}

// SocialSchemaRevision hashes semantics, not a previously assigned revision.
func SocialSchemaRevision(s SocialSchema) string {
	s.Revision = ""
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

var socialUsername = regexp.MustCompile(`^@?[A-Za-z0-9_.-]{1,128}$`)

// ValidateSocialParameters returns an isolated exact-value copy. A nil quantity
// is valid for packages and per-event ranges; no default quantity is fabricated.
func ValidateSocialParameters(s SocialSchema, in SocialParameters) (SocialParameters, *int64, error) {
	if s.Version != 1 {
		return nil, nil, fmt.Errorf("unsupported service schema version")
	}
	out := make(SocialParameters, len(in))
	fields := make(map[string]SocialField, len(s.Fields))
	for _, f := range s.Fields {
		switch f.Key {
		case "key", "action", "service", "order", "orders", "refill", "refills":
			return nil, nil, fmt.Errorf("reserved service field key")
		}
		if f.Key == "" || fields[f.Key].Key != "" {
			return nil, nil, fmt.Errorf("invalid service field key")
		}
		fields[f.Key] = f
		value, exists := in[f.Key]
		if !exists {
			if f.Required {
				return nil, nil, fmt.Errorf("%s is required", f.Key)
			}
			continue
		}
		if f.Required && strings.TrimSpace(value) == "" {
			return nil, nil, fmt.Errorf("%s is required", f.Key)
		}
		if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') || (f.MaxLength > 0 && utf8.RuneCountInString(value) > f.MaxLength) {
			return nil, nil, fmt.Errorf("%s has invalid length or encoding", f.Key)
		}
		switch f.Type {
		case "text":
		case "url":
			u, err := url.Parse(value)
			if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || strings.ContainsAny(value, "\r\n\t ") {
				return nil, nil, fmt.Errorf("%s must be an HTTP URL", f.Key)
			}
		case "username":
			if !socialUsername.MatchString(value) {
				return nil, nil, fmt.Errorf("%s must be a username", f.Key)
			}
		case "integer":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || !socialBounds(n, f.Min, f.Max) {
				return nil, nil, fmt.Errorf("%s is outside its integer range", f.Key)
			}
		case "lines":
			n := socialLineCount(value)
			if s.Quantity.Mode == "derived_lines" && s.Quantity.LinesField == f.Key && s.Quantity.LinePolicy == "all" {
				n = int64(len(strings.Split(value, "\n")))
			}
			if f.Key == s.Quantity.LinesField && s.Quantity.LinePolicy == "all" {
				n = int64(len(strings.Split(value, "\n")))
			}
			if !socialBounds(n, f.MinItems, f.MaxItems) {
				return nil, nil, fmt.Errorf("%s has invalid line count", f.Key)
			}
		case "date":
			if _, err := time.Parse("2/1/2006", value); err != nil {
				return nil, nil, fmt.Errorf("%s must be DD/MM/YYYY", f.Key)
			}
		case "enum":
			found := false
			for _, option := range f.Options {
				found = found || value == option
			}
			if !found {
				return nil, nil, fmt.Errorf("%s has invalid selection", f.Key)
			}
		default:
			return nil, nil, fmt.Errorf("unsupported service field type")
		}
		out[f.Key] = value
	}
	for key := range in {
		if _, ok := fields[key]; !ok {
			return nil, nil, fmt.Errorf("unknown service field %s", key)
		}
	}
	var quantity *int64
	q := s.Quantity
	switch q.Mode {
	case "explicit":
		key := q.Field
		if key == "" {
			key = "quantity"
		}
		value, exists := out[key]
		if !exists {
			return nil, nil, fmt.Errorf("%s is required", key)
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid service quantity")
		}
		quantity = &n
	case "derived_lines":
		if q.LinesField == "" {
			return nil, nil, fmt.Errorf("missing service line field")
		}
		value, exists := out[q.LinesField]
		if !exists {
			return nil, nil, fmt.Errorf("%s is required", q.LinesField)
		}
		var n int64
		switch q.LinePolicy {
		case "nonempty":
			n = socialLineCount(value)
		case "all":
			n = int64(len(strings.Split(value, "\n")))
		default:
			return nil, nil, fmt.Errorf("service line quantity policy has not been verified")
		}
		quantity = &n
	case "absent":
	case "range_per_event":
		lo, e1 := strconv.ParseInt(out[q.MinField], 10, 64)
		hi, e2 := strconv.ParseInt(out[q.MaxField], 10, 64)
		if q.MinField == "" || q.MaxField == "" || e1 != nil || e2 != nil || lo > hi || !socialBounds(lo, q.Min, q.Max) || !socialBounds(hi, q.Min, q.Max) {
			return nil, nil, fmt.Errorf("invalid per-event quantity range")
		}
	default:
		return nil, nil, fmt.Errorf("unsupported service quantity mode")
	}
	if quantity != nil {
		if !socialBounds(*quantity, q.Min, q.Max) {
			return nil, nil, fmt.Errorf("service quantity outside provider range")
		}
		if q.Step != nil {
			base := int64(0)
			if q.StepBase != nil {
				base = *q.StepBase
			}
			if *q.Step <= 0 || *quantity < base || (*quantity-base)%*q.Step != 0 {
				return nil, nil, fmt.Errorf("invalid service quantity step")
			}
		}
	}
	return out, quantity, nil
}

func socialBounds(v int64, min, max *int64) bool {
	return (min == nil || v >= *min) && (max == nil || v <= *max)
}
func socialLineCount(s string) int64 {
	var n int64
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}
