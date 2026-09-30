package port

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

const SMSDelivery = "sms_activation"
const SMSCapability = "sms_activation.v1"
const SMSPolling = "sms_polling.v1"

// Integer accepts protobuf decimal strings and JSON integers without a float conversion.
type Integer int64

func (v *Integer) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid protocol integer")
	}
	*v = Integer(n)
	return nil
}
func (v Integer) MarshalJSON() ([]byte, error) { return json.Marshal(strconv.FormatInt(int64(v), 10)) }

type SMSSnapshot struct {
	SessionID           string  `json:"session_id"`
	State               string  `json:"state"`
	Version             Integer `json:"version"`
	PhoneNumber         string  `json:"phone_number"`
	ExpiresAt           string  `json:"expires_at"`
	OTPCode             string  `json:"otp_code"`
	OTPMessage          string  `json:"otp_message"`
	SMSRevision         Integer `json:"sms_revision"`
	OTPReceivedAt       string  `json:"otp_received_at"`
	CanCancel           bool    `json:"can_cancel"`
	CanFinish           bool    `json:"can_finish"`
	CancelAvailableAt   string  `json:"cancel_available_at"`
	SettlementState     string  `json:"settlement_state"`
	PaidAmountCents     Integer `json:"paid_amount_cents"`
	RefundedAmountCents Integer `json:"refunded_amount_cents"`
	Currency            string  `json:"currency"`
	RefundReference     string  `json:"refund_reference"`
	ErrorCode           string  `json:"error_code"`
}
type SMSPurchase struct {
	ProductID            string  `json:"product_id"`
	Quantity             int     `json:"quantity"`
	DownstreamOrderNo    string  `json:"downstream_order_no"`
	RequiredCapability   string  `json:"required_capability"`
	MaxSupplyAmountCents Integer `json:"max_supply_amount_cents"`
	Currency             string  `json:"currency"`
}
type SMSFulfillment struct {
	Kind string       `json:"kind"`
	SMS  *SMSSnapshot `json:"sms"`
}
type SMSOrder struct {
	SupplyOrderID     string          `json:"supply_order_id"`
	DownstreamOrderNo string          `json:"downstream_order_no"`
	Status            string          `json:"status"`
	Amount            Integer         `json:"amount"`
	Charged           bool            `json:"charged"`
	ErrorCode         string          `json:"error_code"`
	Fulfillment       *SMSFulfillment `json:"fulfillment"`
}
type SMSOperation struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	ErrorCode   string `json:"error_code"`
}

// SMSClient is separate from card purchasing and its callback/refund endpoints.
type SMSClient interface {
	CreateSMS(context.Context, SMSPurchase) (*SMSOrder, error)
	QuerySMS(context.Context, []string) ([]SMSOrder, error)
	ActSMS(context.Context, string, string, string) (*SMSOperation, error)
}

type SMSQuote struct {
	ProductID    string
	ConnectionID uint64
	Identity     string
	Amount       int64
}
type SMSGateway interface {
	PrepareSMS(context.Context, uint64, string) (SMSQuote, error)
	OpenSMS(context.Context, uint64, string) (SMSClient, error)
}
