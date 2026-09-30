package data

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/smsintent"
	"os"
)

func SMSSalesEnabled() bool { return os.Getenv("ZCARD_SMS_SALES_ENABLED") == "true" }
func SMSConnectionIdentity(c *ent.SupplyConnection) string {
	b, _ := json.Marshal([]any{c.ID, c.Driver, c.BaseURL, c.Credentials})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// FrozenSMS contains only the public request and original connection identity, never credentials.
type FrozenSMS struct {
	Description  string `json:"description,omitempty"`
	ConnectionID uint64 `json:"connection_id"`
	Identity     string `json:"identity"`
	Request      string `json:"request"`
	Hash         string `json:"hash"`
	RequestNo    string `json:"request_no"`
}

func HasSMSOrder(ctx context.Context, c *ent.Client, id uint64) (bool, error) {
	return c.OrderItem.Query().Where(orderitem.OrderID(id), orderitem.DeliveryKind("sms_activation")).Exist(ctx)
}
func GuardSMSConnection(ctx context.Context, c *ent.Client, id uint64) error {
	busy, err := c.SMSIntent.Query().Where(smsintent.ConnectionID(id), smsintent.PhaseNEQ("done")).Exist(ctx)
	if err != nil {
		return err
	}
	if busy {
		return fmt.Errorf("货源仍有待确认的接码订单，不能修改账号身份或删除")
	}
	return nil
}
func SMSRefundConfirmed(s *ent.SMSIntent) bool {
	return s.RejectedReceipt && s.ChargedAmount == 0 && s.UpstreamOrderID != nil || (s.ChargedAmount > 0 && s.SettlementState == "refunded" && s.RefundedAmount == s.ChargedAmount && s.RefundReference != "" && s.UpstreamOrderID != nil)
}

// GuardSMSRefund coordinates manual refunds with the independently recovered upstream obligation.
func GuardSMSRefund(ctx context.Context, c *ent.Client, orderID uint64) error {
	rows, err := c.SMSIntent.Query().Where(smsintent.OrderID(orderID)).All(ctx)
	if err != nil {
		return err
	}
	for _, s := range rows {
		if !SMSRefundConfirmed(s) {
			return fmt.Errorf("接码供货结果尚未确认退款，不能退还零售余额")
		}
	}
	has, err := HasSMSOrder(ctx, c, orderID)
	if err != nil {
		return err
	}
	if has && len(rows) == 0 {
		return fmt.Errorf("接码采购凭证缺失，请核对")
	}
	return nil
}

func ValidateSMSPayment(ctx context.Context, c *ent.Client, orderID uint64) error {
	items, err := c.OrderItem.Query().Where(orderitem.OrderID(orderID), orderitem.DeliveryKind("sms_activation")).All(ctx)
	if err != nil {
		return err
	}
	for _, it := range items {
		var f FrozenSMS
		if json.Unmarshal([]byte(it.SmsPurchaseSnapshot), &f) != nil {
			return fmt.Errorf("接码购买意图无效")
		}
		conn, e := c.SupplyConnection.Get(ctx, f.ConnectionID)
		if e != nil {
			return e
		}
		if string(conn.Status) != "active" || SMSConnectionIdentity(conn) != f.Identity {
			return fmt.Errorf("接码货源账号已变化或停用，请重新购买")
		}
	}
	return nil
}
