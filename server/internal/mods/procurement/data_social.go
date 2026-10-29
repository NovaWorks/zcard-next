package procurement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialoperation"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialtask"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/google/uuid"
	"time"
)

func socialTerminal(state string) bool {
	return state == "completed" || state == "partial" || state == "canceled" || state == "rejected" || state == "failed"
}
func (s *ProcureService) RunSocial(ctx context.Context) {
	gw, ok := s.gw.(supplyport.SocialClientGateway)
	if !ok {
		return
	}
	c := data.Client(ctx, s.repo.data)
	rows, e := c.SocialTask.Query().Where(socialtask.Role("retail"), socialtask.PhaseNotIn("done", "review"), socialtask.NextRunAtLTE(time.Now().Unix()), socialtask.LeaseUntilLTE(time.Now().Unix())).Order(ent.Asc(socialtask.FieldNextRunAt)).Limit(20).All(ctx)
	if e != nil {
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		token := uuid.NewString()
		n, e := c.SocialTask.Update().Where(socialtask.ID(row.ID), socialtask.Role("retail"), socialtask.Version(row.Version), socialtask.Phase(row.Phase), socialtask.LeaseUntilLTE(time.Now().Unix())).SetLeaseToken(token).SetLeaseUntil(time.Now().Add(90 * time.Second).Unix()).Save(ctx)
		if e != nil || n != 1 {
			continue
		}
		row.LeaseToken = token
		run, cancel := context.WithTimeout(ctx, 60*time.Second)
		e = s.runSocialTask(run, row, gw)
		cancel()
		if e != nil {
			s.socialFailure(context.WithoutCancel(ctx), row, e)
		}
	}
}
func (s *ProcureService) runSocialTask(ctx context.Context, row *ent.SocialTask, gw supplyport.SocialClientGateway) error {
	if row.Phase == "refund" {
		return s.socialRefund(ctx, row)
	}
	c := data.Client(ctx, s.repo.data)
	conn, e := c.SupplyConnection.Get(ctx, row.ConnectionID)
	if e != nil {
		return e
	}
	if data.SocialConnectionIdentity(conn) != row.ConnectionIdentity {
		return s.socialReview(ctx, row, "account_changed")
	}
	if row.UpstreamOrderID == "" && string(conn.Status) != "active" {
		return s.socialReview(ctx, row, "connection_disabled_before_purchase")
	}
	client, e := gw.OpenSocial(ctx, row.ConnectionID)
	if e != nil {
		return e
	}
	if row.UpstreamOrderID == "" {
		if s.cipher == nil {
			return fmt.Errorf("social cipher unavailable")
		}
		raw, e := s.cipher.Open(row.RequestCipher, row.OrderItemID, row.SubsiteID)
		if e != nil {
			return e
		}
		var req supplyport.SocialPurchase
		if json.Unmarshal([]byte(raw), &req) != nil || fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) != row.RequestHash || req.DownstreamOrderNo != row.RequestNo || req.ProductID != row.UpstreamProductID {
			return s.socialReview(ctx, row, "invalid_frozen_request")
		}
		// The A-side API is idempotent by this original request number. No retry may
		// create another local task, quote, request number, or purchase parameters.
		result, e := client.CreateSocial(ctx, req)
		if e != nil {
			return e
		}
		if result == nil || result.SupplyOrderID == "" || result.DownstreamOrderNo != row.RequestNo {
			return s.socialReview(ctx, row, "invalid_receipt")
		}
		rejected := result.Status == "rejected" && !result.Charged && result.Amount == 0 && result.ErrorCode != ""
		if !rejected && (!result.Charged || result.Amount <= 0 || result.Amount > req.MaxSupplyAmountCents) {
			return s.socialReview(ctx, row, "invalid_receipt_amount")
		}
		finish, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer done()
		q := data.Client(finish, s.repo.data).SocialTask.Update().Where(socialtask.ID(row.ID), socialtask.LeaseToken(row.LeaseToken)).SetUpstreamOrderID(result.SupplyOrderID).SetChargedAmount(int64(result.Amount)).SetPhase("query").SetState("pending").SetAttempts(0).SetLastError("").SetLeaseUntil(0).SetNextRunAt(time.Now().Add(3 * time.Second).Unix())
		if rejected {
			q.SetState("rejected").SetPhase("refund").SetSettlementState("refunded").SetRefundReference("rejected:" + result.SupplyOrderID).SetRetailRefundState("pending")
		}
		return q.Exec(finish)
	}
	op, e := c.SocialOperation.Query().Where(socialoperation.TaskID(row.ID), socialoperation.StatusIn("pending", "unknown")).Order(ent.Asc(socialoperation.FieldCreatedAt)).First(ctx)
	if e != nil && !ent.IsNotFound(e) {
		return e
	}
	if op != nil {
		if e := s.socialOperation(ctx, row, op, client); e != nil {
			return e
		}
	}
	orders, e := client.QuerySocial(ctx, []string{row.UpstreamOrderID})
	if e != nil {
		return e
	}
	if len(orders) != 1 || orders[0].SupplyOrderID != row.UpstreamOrderID || orders[0].DownstreamOrderNo != row.RequestNo || int64(orders[0].Amount) != row.ChargedAmount || orders[0].Snapshot == nil {
		return s.socialReview(ctx, row, "order_identity_mismatch")
	}
	return s.socialSnapshot(ctx, row, orders[0].Snapshot)
}
func (s *ProcureService) socialOperation(ctx context.Context, row *ent.SocialTask, op *ent.SocialOperation, client supplyport.SocialClient) error {
	var result *supplyport.SocialOperation
	var e error
	if op.UpstreamOperationID != "" {
		result, e = client.QuerySocialOperation(ctx, row.UpstreamOrderID, op.OperationID)
	} else {
		result, e = client.ActSocial(ctx, row.UpstreamOrderID, op.Action, op.OperationID)
	}
	if e != nil {
		return e
	}
	if result == nil || result.OperationID != op.OperationID {
		return fmt.Errorf("invalid social operation receipt")
	}
	switch result.Status {
	case "pending", "succeeded", "rejected", "unknown":
	default:
		return fmt.Errorf("unknown social operation status")
	}
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		task, e := c.SocialTask.Get(ctx, row.ID)
		if e != nil {
			return e
		}
		if task.LeaseToken != row.LeaseToken || task.LeaseUntil <= time.Now().Unix() {
			return fmt.Errorf("social operation lease lost")
		}
		current, e := c.SocialOperation.Get(ctx, op.ID)
		if e != nil {
			return e
		}
		if current.Status == "succeeded" || current.Status == "rejected" {
			return nil
		}
		return c.SocialOperation.UpdateOneID(op.ID).SetUpstreamOperationID(result.OperationID).SetStatus(result.Status).SetErrorCode(result.ErrorCode).Exec(ctx)
	})
}
func (s *ProcureService) socialSnapshot(ctx context.Context, row *ent.SocialTask, snap *supplyport.SocialSnapshot) error {
	if snap.Version <= 0 || snap.Currency != "CNY" || int64(snap.PaidAmountCents) != row.ChargedAmount || snap.RefundedAmountCents < 0 || snap.RefundedAmountCents > snap.PaidAmountCents {
		return s.socialReview(ctx, row, "invalid_settlement_amount")
	}
	switch snap.State {
	case "pending", "queued", "processing", "in_progress", "submit_unknown", "completed", "partial", "canceled", "rejected", "failed", "review":
	default:
		return s.socialReview(ctx, row, "unknown_service_state")
	}
	switch snap.SettlementState {
	case "paid", "review":
		if snap.RefundedAmountCents != 0 {
			return s.socialReview(ctx, row, "unconfirmed_refund")
		}
	case "partially_refunded":
		if snap.RefundedAmountCents <= 0 || snap.RefundedAmountCents >= snap.PaidAmountCents || snap.RefundReference == "" {
			return s.socialReview(ctx, row, "invalid_partial_refund")
		}
	case "refunded":
		if snap.RefundedAmountCents != snap.PaidAmountCents || snap.RefundReference == "" {
			return s.socialReview(ctx, row, "invalid_full_refund")
		}
	default:
		return s.socialReview(ctx, row, "unknown_settlement")
	}
	for _, n := range []*int64{snap.Quantity, snap.StartCount, snap.Remains, snap.CompletedQuantity} {
		if n != nil && *n < 0 {
			return s.socialReview(ctx, row, "invalid_progress")
		}
	}
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		current, e := c.SocialTask.Get(ctx, row.ID)
		if e != nil {
			return e
		}
		if current.LeaseToken != row.LeaseToken || current.LeaseUntil <= time.Now().Unix() {
			return fmt.Errorf("social lease lost")
		}
		if int64(snap.Version) < current.UpstreamVersion {
			return s.socialReschedule(ctx, row, 10*time.Second)
		}
		if int64(snap.RefundedAmountCents) < current.RefundedAmount {
			return s.socialReview(ctx, row, "refund_regressed")
		}
		if int64(snap.RefundedAmountCents) > current.RefundedAmount && current.RefundReference != "" && current.RefundReference == snap.RefundReference {
			return s.socialReview(ctx, row, "refund_reference_reused")
		}
		raw, _ := json.Marshal(snap)
		if len(current.SnapshotCipher) > 0 {
			old, e := s.cipher.Open(current.SnapshotCipher, current.OrderItemID, current.SubsiteID)
			if e != nil {
				return e
			}
			if int64(snap.Version) == current.UpstreamVersion && !bytes.Equal([]byte(old), raw) {
				return s.socialReview(ctx, row, "same_version_changed")
			}
		}
		sealed, e := s.cipher.Seal(string(raw), current.OrderItemID, current.SubsiteID)
		if e != nil {
			return e
		}
		phase := "query"
		refundStatus := current.RetailRefundState
		if int64(snap.RefundedAmountCents) > current.RefundedAmount {
			phase = "refund"
			refundStatus = "pending"
		}
		if phase == "query" && snap.SettlementState == "refunded" && current.RetailPaidAmount > 0 && current.RetailRefundedAmount == current.RetailPaidAmount && refundStatus == "succeeded" {
			pending, e := c.SocialOperation.Query().Where(socialoperation.TaskID(current.ID), socialoperation.StatusIn("pending", "unknown")).Exist(ctx)
			if e != nil {
				return e
			}
			if !pending {
				phase = "done"
			}
		}
		delay := 10 * time.Second
		if socialTerminal(snap.State) && snap.SettlementState == "paid" {
			delay = time.Minute
		}
		nextRun := time.Now().Add(delay).Unix()
		if phase == "refund" || phase == "done" {
			nextRun = 0
		}
		n, e := c.SocialTask.Update().Where(socialtask.ID(current.ID), socialtask.LeaseToken(row.LeaseToken), socialtask.UpstreamVersion(current.UpstreamVersion)).SetUpstreamVersion(int64(snap.Version)).AddVersion(1).SetSnapshotCipher(sealed).SetState(snap.State).SetSettlementState(snap.SettlementState).SetRefundedAmount(int64(snap.RefundedAmountCents)).SetRefundReference(snap.RefundReference).SetCanCancel(snap.CanCancel).SetCanRefill(snap.CanRefill).SetRetailRefundState(refundStatus).SetPhase(phase).SetLeaseUntil(0).SetNextRunAt(nextRun).SetAttempts(0).SetLastError("").Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("social snapshot raced")
		}
		if snap.State == "completed" || snap.State == "partial" {
			return s.socialDelivered(ctx, current, snap.State)
		}
		return nil
	})
}
func (s *ProcureService) socialDelivered(ctx context.Context, row *ent.SocialTask, state string) error {
	c := data.Client(ctx, s.repo.data)
	o, e := c.Order.Get(ctx, row.OrderID)
	if e != nil {
		return e
	}
	if o.UserID != row.UserID || o.SubsiteID != row.SubsiteID {
		return fmt.Errorf("social order ownership mismatch")
	}
	if o.Status == order.StatusRefunded || o.Status == order.StatusDelivered || o.Status == order.StatusCompleted {
		return nil
	}
	target := order.StatusDelivered
	if state == "partial" {
		target = order.StatusPartiallyDelivered
	}
	if o.Status == target {
		return nil
	}
	if o.Status != order.StatusPaid && o.Status != order.StatusFulfilling && o.Status != order.StatusPartiallyDelivered {
		return fmt.Errorf("unexpected social retail order status")
	}
	n, e := c.Order.Update().Where(order.ID(o.ID), order.Version(o.Version)).SetStatus(target).AddVersion(1).Save(ctx)
	if e != nil {
		return e
	}
	if n != 1 {
		return fmt.Errorf("social order raced")
	}
	status := "delivered"
	if state == "partial" {
		status = "partial"
	}
	if e = c.OrderItem.UpdateOneID(row.OrderItemID).SetFulfillmentStatus(status).Exec(ctx); e != nil {
		return e
	}
	if e := c.OrderStatusEvent.Create().SetOrderID(o.ID).SetFromStatus(string(o.Status)).SetToStatus(string(target)).SetEvent("social_" + state).SetOperator("system").Exec(ctx); e != nil {
		return e
	}
	if target == order.StatusDelivered && s.outbox != nil {
		payload, _ := json.Marshal(map[string]any{"order_id": o.ID, "order_no": o.OrderNo, "user_id": o.UserID, "subsite_id": o.SubsiteID, "delivery_kind": supplyport.SocialDelivery})
		return s.outbox.Write(ctx, "fulfillment", "order.delivered", o.OrderNo, "order:"+o.OrderNo+":delivered", payload)
	}
	return nil
}
func (s *ProcureService) socialRefund(ctx context.Context, row *ent.SocialTask) error {
	r, ok := s.refund.(interface {
		RefundSocial(context.Context, string) (uint64, error)
	})
	if !ok {
		return fmt.Errorf("social refund service unavailable")
	}
	_, e := r.RefundSocial(ctx, row.ID)
	return e
}
func (s *ProcureService) socialReschedule(ctx context.Context, row *ent.SocialTask, delay time.Duration) error {
	return data.Client(ctx, s.repo.data).SocialTask.Update().Where(socialtask.ID(row.ID), socialtask.LeaseToken(row.LeaseToken)).SetLeaseUntil(0).SetNextRunAt(time.Now().Add(delay).Unix()).Exec(ctx)
}
func (s *ProcureService) socialReview(ctx context.Context, row *ent.SocialTask, code string) error {
	e := data.Client(ctx, s.repo.data).SocialTask.Update().Where(socialtask.ID(row.ID), socialtask.LeaseToken(row.LeaseToken)).SetPhase("review").SetLastError(code).SetCanCancel(false).SetCanRefill(false).SetLeaseUntil(0).Exec(ctx)
	return e
}
func (s *ProcureService) socialFailure(ctx context.Context, row *ent.SocialTask, cause error) {
	finish, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Transport failures remain recoverable: a prolonged outage must not strand
	// a paid obligation merely because an arbitrary retry count was reached.
	delay := time.Duration(5*(1<<min(row.Attempts, 7))) * time.Second
	_ = data.Client(finish, s.repo.data).SocialTask.Update().Where(socialtask.ID(row.ID), socialtask.LeaseToken(row.LeaseToken)).SetLeaseUntil(0).SetAttempts(min(row.Attempts+1, 1000000)).SetLastError("upstream_retrying").SetNextRunAt(time.Now().Add(delay).Unix()).Exec(finish)
}
