package procurement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	si "github.com/NovaWorks/zcard-next/server/internal/data/ent/smsintent"
	so "github.com/NovaWorks/zcard-next/server/internal/data/ent/smsoperation"
	sc "github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/google/uuid"
	"time"
)

var errSMSReconciliation = errors.New("sms reconciliation required")

// RunSMS is database-backed and independent of the sales switch, browser, and Redis.
// Claim batches by original account identity, with a bounded lease and fenced writes.
func (s *ProcureService) RunSMS(ctx context.Context) {
	gateway, ok := s.gw.(supplyport.SMSGateway)
	if !ok {
		return
	}
	c := data.Client(ctx, s.repo.data)
	rows, err := c.SMSIntent.Query().Where(si.PhaseNotIn("done", "review"), si.NextRunAtLTE(time.Now().Unix()), si.LeaseUntilLTE(time.Now().Unix())).Order(ent.Asc(si.FieldNextRunAt)).Limit(50).All(ctx)
	if err != nil {
		return
	}
	groups := map[string][]*ent.SMSIntent{}
	for _, row := range rows {
		key := fmt.Sprintf("%d:%s", row.ConnectionID, row.ConnectionIdentity)
		groups[key] = append(groups[key], row)
	}
	for _, batch := range groups {
		if ctx.Err() != nil {
			return
		}
		lease := uuid.NewString()
		n, e := c.SupplyConnection.Update().Where(sc.ID(batch[0].ConnectionID), sc.SmsLeaseUntilLTE(time.Now().Unix())).SetSmsLeaseToken(lease).SetSmsLeaseUntil(time.Now().Add(2 * time.Minute).Unix()).Save(ctx)
		if e != nil || n != 1 {
			continue
		}
		release := func() {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer done()
			_ = c.SupplyConnection.Update().Where(sc.ID(batch[0].ConnectionID), sc.SmsLeaseToken(lease)).SetSmsLeaseUntil(0).Exec(cleanup)
		}
		claimed := []*ent.SMSIntent{}
		for _, row := range batch {
			n, e := c.SMSIntent.Update().Where(si.ID(row.ID), si.NextRunAtLTE(time.Now().Unix()), si.LeaseUntilLTE(time.Now().Unix()), si.PhaseNotIn("done", "review")).SetLeaseToken(lease).SetLeaseUntil(time.Now().Add(2 * time.Minute).Unix()).Save(ctx)
			if e == nil && n == 1 {
				row.LeaseToken = lease
				claimed = append(claimed, row)
			}
		}
		if len(claimed) == 0 {
			release()
			continue
		}
		// Sequential per connection: later rows are not leased while unbounded work runs.
		run, cancel := context.WithTimeout(ctx, 90*time.Second)
		client, e := gateway.OpenSMS(run, claimed[0].ConnectionID, claimed[0].ConnectionIdentity)
		query := []*ent.SMSIntent{}
		for _, row := range claimed {
			if row.Phase == "refund" {
				s.smsRefund(run, row)
				continue
			}
			if e != nil {
				s.smsFailure(ctx, row, e)
				continue
			}
			switch row.Phase {
			case "purchase":
				s.smsPurchase(run, row, client)
			case "refund":
				s.smsRefund(run, row)
			default:
				query = append(query, row)
			}
		}
		s.smsQueryBatch(run, client, query)
		cancel()
		release()
	}
}
func (s *ProcureService) smsPurchase(ctx context.Context, row *ent.SMSIntent, client supplyport.SMSClient) {
	var req supplyport.SMSPurchase
	if fmt.Sprintf("%x", sha256.Sum256([]byte(row.RequestJSON))) != row.RequestHash || json.Unmarshal([]byte(row.RequestJSON), &req) != nil || req.DownstreamOrderNo != row.RequestNo {
		s.smsReview(ctx, row, "invalid_intent")
		return
	}
	result, err := client.CreateSMS(ctx, req)
	if err != nil {
		s.smsFailure(ctx, row, err)
		return
	}
	if result == nil || result.SupplyOrderID == "" {
		s.smsFailure(ctx, row, fmt.Errorf("invalid_receipt"))
		return
	}
	err = data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		q := c.SMSIntent.Update().Where(si.ID(row.ID), si.LeaseToken(row.LeaseToken), si.LeaseUntilGT(time.Now().Unix())).SetUpstreamOrderID(result.SupplyOrderID).SetChargedAmount(int64(result.Amount)).SetAttempts(0).SetLastError("").SetNextRunAt(time.Now().Add(8 * time.Second).Unix()).SetLeaseUntil(0)
		if result.Status == "rejected" && !result.Charged && result.Amount == 0 && result.ErrorCode != "" {
			q.SetRejectedReceipt(true).SetLastError(result.ErrorCode).SetState("rejected").SetPhase("refund").SetRetailRefundState("pending")
		} else if result.Charged && result.Amount > 0 && result.Amount <= req.MaxSupplyAmountCents {
			q.SetPhase("query")
		} else {
			return fmt.Errorf("invalid_receipt")
		}
		n, e := q.Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("lease_lost")
		}
		return nil
	})
	if err != nil {
		s.smsFailure(ctx, row, err)
	}
}
func (s *ProcureService) smsQueryBatch(ctx context.Context, client supplyport.SMSClient, rows []*ent.SMSIntent) {
	if len(rows) == 0 || client == nil {
		return
	}
	ids := []string{}
	valid := make([]*ent.SMSIntent, 0, len(rows))
	for _, row := range rows {
		if row.UpstreamOrderID == nil {
			s.smsReview(ctx, row, "missing_supply_order")
			continue
		}
		ids = append(ids, *row.UpstreamOrderID)
		valid = append(valid, row)
	}
	rows = valid
	if len(rows) == 0 {
		return
	}
	orders, err := client.QuerySMS(ctx, ids)
	if err != nil {
		var fault interface {
			SMSFailure() (int, string, time.Duration)
		}
		accountFailure := ctx.Err() != nil
		if errors.As(err, &fault) {
			status, _, _ := fault.SMSFailure()
			accountFailure = accountFailure || status == 429 || status == 401 || status == 403
		}
		if accountFailure {
			for _, row := range rows {
				s.smsFailure(ctx, row, err)
			}
			return
		}
		if len(rows) > 1 {
			mid := len(rows) / 2
			s.smsQueryBatch(ctx, client, rows[:mid])
			s.smsQueryBatch(ctx, client, rows[mid:])
			return
		}
		s.smsFailure(ctx, rows[0], err)
		return
	}
	byID := map[string]supplyport.SMSOrder{}
	for _, o := range orders {
		byID[o.SupplyOrderID] = o
	}
	for _, row := range rows {
		o, ok := byID[*row.UpstreamOrderID]
		if !ok || o.DownstreamOrderNo != row.RequestNo || int64(o.Amount) != row.ChargedAmount || o.Fulfillment == nil || o.Fulfillment.Kind != supplyport.SMSDelivery || o.Fulfillment.SMS == nil {
			s.smsReview(ctx, row, "order_identity_or_fulfillment_mismatch")
			continue
		}
		// Replay a persisted pending operation independently of the session state.
		opErr := s.smsReplayOperation(ctx, row, client)
		if err := s.smsSnapshot(ctx, row, o.Fulfillment.SMS); err != nil {
			if errors.Is(err, errSMSReconciliation) {
				s.smsReview(ctx, row, "snapshot_reconciliation_required")
			} else {
				s.smsFailure(ctx, row, err)
			}
		} else if opErr != nil {
			// Session settlement takes precedence over an independent action failure.
			current, e := data.Client(ctx, s.repo.data).SMSIntent.Get(ctx, row.ID)
			if e == nil && current.LeaseToken == row.LeaseToken && current.Phase == "query" {
				s.smsFailure(ctx, row, opErr)
			}
		}
	}
}
func (s *ProcureService) smsReplayOperation(ctx context.Context, row *ent.SMSIntent, client supplyport.SMSClient) error {
	c := data.Client(ctx, s.repo.data)
	op, err := c.SMSOperation.Query().Where(so.IntentID(row.ID), so.Status("pending")).Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	result, actionErr := client.ActSMS(ctx, *row.UpstreamOrderID, op.Action, op.OperationID)
	if actionErr == nil && (result == nil || result.OperationID != op.OperationID) {
		actionErr = fmt.Errorf("operation_mismatch")
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	err = data.Tx(finish, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		n, e := c.SMSIntent.Update().Where(si.ID(row.ID), si.LeaseToken(row.LeaseToken), si.LeaseUntilGT(time.Now().Unix())).SetLastError("").Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("lease_lost")
		}
		q := c.SMSOperation.Update().Where(so.ID(op.ID), so.Status("pending"))
		if actionErr != nil {
			q.AddAttempts(1)
			review := op.Attempts >= 12
			var fault interface {
				SMSFailure() (int, string, time.Duration)
			}
			if errors.As(actionErr, &fault) {
				status, reason, _ := fault.SMSFailure()
				review = review || status == 409 && reason != "supply.ALLOCATING"
			}
			if review {
				q.SetStatus("review").SetErrorCode("operation_reconciliation_required")
			}
		} else {
			q.SetStatus(result.Status).SetErrorCode(result.ErrorCode).SetAttempts(0)
		}
		return q.Exec(ctx)
	})
	if err != nil {
		return err
	}
	return actionErr
}
func (s *ProcureService) smsSnapshot(ctx context.Context, row *ent.SMSIntent, snap *supplyport.SMSSnapshot) error {
	if snap.SessionID == "" || snap.Version <= 0 || snap.SMSRevision < 0 || snap.Currency != "CNY" || int64(snap.PaidAmountCents) != row.ChargedAmount {
		return fmt.Errorf("%w: invalid_snapshot", errSMSReconciliation)
	}
	terminal := false
	switch snap.State {
	case "allocating", "waiting_sms", "sms_received":
	case "completed", "canceled", "expired", "rejected":
		terminal = true
	default:
		return fmt.Errorf("%w: unknown_state", errSMSReconciliation)
	}
	if snap.SettlementState != "paid" && snap.SettlementState != "refunded" {
		return fmt.Errorf("%w: unknown_settlement", errSMSReconciliation)
	}
	if snap.SettlementState == "refunded" && (snap.RefundReference == "" || snap.RefundedAmountCents != snap.PaidAmountCents) {
		return fmt.Errorf("%w: invalid_refund", errSMSReconciliation)
	}
	if snap.SettlementState == "paid" && snap.RefundedAmountCents != 0 {
		return fmt.Errorf("%w: invalid_settlement", errSMSReconciliation)
	}
	for _, v := range []string{snap.ExpiresAt, snap.OTPReceivedAt, snap.CancelAvailableAt} {
		if v != "" {
			if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
				return fmt.Errorf("%w: invalid_time", errSMSReconciliation)
			}
		}
	}
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		current, err := c.SMSIntent.Get(ctx, row.ID)
		if err != nil {
			return err
		}
		if current.LeaseToken != row.LeaseToken || current.LeaseUntil <= time.Now().Unix() {
			return fmt.Errorf("lease_lost")
		}
		if current.SessionID != "" && current.SessionID != snap.SessionID {
			return fmt.Errorf("%w: session_mismatch", errSMSReconciliation)
		}
		if int64(snap.Version) < current.Version {
			return s.smsReschedule(ctx, current, 8*time.Second)
		}
		if int64(snap.SMSRevision) < current.SmsRevision {
			return fmt.Errorf("%w: sms_revision_regressed", errSMSReconciliation)
		}
		copy := *snap
		if len(current.SnapshotCipher) > 0 {
			plain, e := s.cipher.Open(current.SnapshotCipher, current.OrderItemID, current.SubsiteID)
			if e != nil {
				return e
			}
			var old supplyport.SMSSnapshot
			if json.Unmarshal([]byte(plain), &old) != nil {
				return fmt.Errorf("%w: invalid_local_snapshot", errSMSReconciliation)
			}
			if (old.State == "completed" || old.State == "expired" || old.State == "canceled" || old.State == "rejected") && old.State != snap.State {
				return fmt.Errorf("%w: terminal_regressed", errSMSReconciliation)
			}
			if current.Received && (snap.State == "waiting_sms" || snap.State == "allocating") {
				return fmt.Errorf("%w: delivery_regressed", errSMSReconciliation)
			}
			if old.SettlementState == "refunded" && snap.SettlementState != "refunded" {
				return fmt.Errorf("%w: settlement_regressed", errSMSReconciliation)
			}
			if snap.SMSRevision == supplyport.Integer(current.SmsRevision) {
				copy.OTPCode = old.OTPCode
				copy.OTPMessage = old.OTPMessage
				copy.OTPReceivedAt = old.OTPReceivedAt
			}
			if snap.Version == supplyport.Integer(current.Version) {
				a, _ := json.Marshal(old)
				b, _ := json.Marshal(copy)
				if !bytes.Equal(a, b) {
					return fmt.Errorf("%w: same_version_changed", errSMSReconciliation)
				}
			}
			if current.Received && (copy.OTPCode == "" && copy.OTPMessage == "") {
				return fmt.Errorf("%w: sms_content_lost", errSMSReconciliation)
			}

		}
		received := copy.OTPCode != "" || copy.OTPMessage != ""
		if copy.State == "sms_received" && !received {
			return fmt.Errorf("%w: empty_sms", errSMSReconciliation)
		}
		if terminal && copy.SettlementState == "paid" && !received {
			return fmt.Errorf("%w: terminal_without_delivery", errSMSReconciliation)
		}
		raw, _ := json.Marshal(copy)
		sealed, err := s.cipher.Seal(string(raw), current.OrderItemID, current.SubsiteID)
		if err != nil {
			return err
		}
		phase := "query"
		refundState := current.RetailRefundState
		pending, err := c.SMSOperation.Query().Where(so.IntentID(row.ID), so.Status("pending")).Exist(ctx)
		if err != nil {
			return err
		}
		review, err := c.SMSOperation.Query().Where(so.IntentID(row.ID), so.Status("review")).Exist(ctx)
		if err != nil {
			return err
		}
		if copy.SettlementState == "refunded" {
			phase = "refund"
			refundState = "pending"
		} else if review {
			phase = "review"
		} else if terminal && !pending {
			phase = "done"
		}
		n, err := c.SMSIntent.Update().Where(si.ID(row.ID), si.Version(current.Version), si.LeaseToken(row.LeaseToken)).SetSessionID(copy.SessionID).SetVersion(int64(copy.Version)).SetSmsRevision(int64(copy.SMSRevision)).SetSnapshotCipher(sealed).SetReceived(received).SetState(copy.State).SetCanCancel(copy.CanCancel).SetCanFinish(copy.CanFinish).SetSettlementState(copy.SettlementState).SetRefundedAmount(int64(copy.RefundedAmountCents)).SetRefundReference(copy.RefundReference).SetPhase(phase).SetRetailRefundState(refundState).SetAttempts(0).SetLastError("").SetLeaseUntil(0).SetNextRunAt(time.Now().Add(8 * time.Second).Unix()).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("snapshot_race")
		}
		if received && !current.Received {
			return s.smsDelivered(ctx, current)
		}
		return nil
	})
}
func (s *ProcureService) smsDelivered(ctx context.Context, row *ent.SMSIntent) error {
	c := data.Client(ctx, s.repo.data)
	o, err := c.Order.Get(ctx, row.OrderID)
	if err != nil {
		return err
	}
	if o.UserID != row.UserID || o.SubsiteID != row.SubsiteID {
		return fmt.Errorf("%w: owner_mismatch", errSMSReconciliation)
	}
	if o.Status != order.StatusPaid && o.Status != order.StatusFulfilling {
		return fmt.Errorf("%w: invalid_retail_state", errSMSReconciliation)
	}
	n, err := c.Order.Update().Where(order.ID(o.ID), order.Version(o.Version)).AddVersion(1).SetStatus(order.StatusDelivered).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("order_race")
	}
	if err = c.OrderItem.Update().Where(orderitem.ID(row.OrderItemID), orderitem.OrderID(o.ID), orderitem.SubsiteID(row.SubsiteID)).SetFulfillmentStatus("delivered").Exec(ctx); err != nil {
		return err
	}
	if err := c.OrderStatusEvent.Create().SetOrderID(o.ID).SetFromStatus(string(o.Status)).SetToStatus("delivered").SetEvent("sms_received").SetOperator("system").Exec(ctx); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"order_id": o.ID, "order_no": o.OrderNo, "user_id": o.UserID, "subsite_id": o.SubsiteID, "delivery_kind": "sms_activation"})
	return data.NewOutboxWriter(s.repo.data).Write(ctx, "fulfillment", "order.delivered", o.OrderNo, "order:"+o.OrderNo+":delivered", payload)
}
func (s *ProcureService) smsReschedule(ctx context.Context, row *ent.SMSIntent, delay time.Duration) error {
	return data.Client(ctx, s.repo.data).SMSIntent.Update().Where(si.ID(row.ID), si.LeaseToken(row.LeaseToken)).SetLeaseUntil(0).SetNextRunAt(time.Now().Add(delay).Unix()).Exec(ctx)
}
func (s *ProcureService) smsReview(ctx context.Context, row *ent.SMSIntent, code string) {
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = data.Client(finish, s.repo.data).SMSIntent.Update().Where(si.ID(row.ID), si.LeaseToken(row.LeaseToken)).SetLeaseUntil(0).SetPhase("review").SetLastError(code).Exec(finish)
}
func (s *ProcureService) smsFailure(ctx context.Context, row *ent.SMSIntent, err error) {
	delay := time.Duration(8*(1<<min(row.Attempts, 6))+int(row.ID%5)) * time.Second
	code := "upstream_retrying"
	var fault interface {
		SMSFailure() (int, string, time.Duration)
	}
	if errors.As(err, &fault) {
		status, reason, retry := fault.SMSFailure()
		if retry > delay {
			delay = retry
		}
		if status == 401 || status == 403 {
			code = "account_unavailable"
		}
		if status == 409 && reason != "supply.ALLOCATING" {
			s.smsReview(ctx, row, "upstream_conflict")
			return
		}
	}
	if row.Attempts >= 12 {
		s.smsReview(ctx, row, "retry_exhausted")
		return
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = data.Client(finish, s.repo.data).SMSIntent.Update().Where(si.ID(row.ID), si.LeaseToken(row.LeaseToken)).SetLeaseUntil(0).SetAttempts(row.Attempts + 1).SetLastError(code).SetNextRunAt(time.Now().Add(delay).Unix()).Exec(finish)
}
func (s *ProcureService) smsRefund(ctx context.Context, row *ent.SMSIntent) {
	refunder, ok := s.refund.(interface {
		RefundSMS(context.Context, uint64) (uint64, error)
	})
	if !ok {
		s.smsReview(ctx, row, "refund_service_unavailable")
		return
	}
	_, err := refunder.RefundSMS(ctx, row.ID)
	if err != nil {
		s.smsFailure(ctx, row, err)
	}
}
