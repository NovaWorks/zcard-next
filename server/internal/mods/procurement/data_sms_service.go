package procurement

import (
	"context"
	"encoding/json"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	si "github.com/NovaWorks/zcard-next/server/internal/data/ent/smsintent"
	so "github.com/NovaWorks/zcard-next/server/internal/data/ent/smsoperation"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"
	"strings"
	"time"
)

type StoreSMSService struct {
	storefrontv1.UnimplementedStoreSMSServiceServer
	svc *ProcureService
}

func NewStoreSMSService(svc *ProcureService) *StoreSMSService { return &StoreSMSService{svc: svc} }
func (s *StoreSMSService) owned(ctx context.Context, no string) (*ent.SMSIntent, error) {
	if tr, ok := transport.FromServerContext(ctx); ok {
		tr.ReplyHeader().Set("Cache-Control", "no-store, private")
	}
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil || claims.Subject == 0 || claims.Realm != authn.RealmUser {
		return nil, errors.Unauthorized("sms.LOGIN_REQUIRED", "请登录查看接码订单")
	}
	c := data.Client(ctx, s.svc.repo.data)
	o, e := c.Order.Query().Where(order.OrderNo(no), order.UserID(claims.Subject), order.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Only(ctx)
	if e != nil {
		return nil, errors.NotFound("sms.NOT_FOUND", "接码订单不存在")
	}
	row, e := c.SMSIntent.Query().Where(si.OrderID(o.ID), si.UserID(claims.Subject), si.SubsiteID(o.SubsiteID)).Only(ctx)
	if e != nil {
		return nil, errors.NotFound("sms.NOT_FOUND", "接码订单尚未付款或不存在")
	}
	return row, nil
}
func (s *StoreSMSService) GetSMS(ctx context.Context, req *storefrontv1.SMSOrderRequest) (*storefrontv1.SMSOrderReply, error) {
	row, e := s.owned(ctx, req.OrderNo)
	if e != nil {
		return nil, e
	}
	return s.reply(ctx, row)
}
func (s *StoreSMSService) reply(ctx context.Context, row *ent.SMSIntent) (*storefrontv1.SMSOrderReply, error) {
	out := &storefrontv1.SMSOrderReply{State: row.State, Phase: row.Phase, RefundStatus: row.RetailRefundState, CanCancel: row.CanCancel, CanFinish: row.CanFinish, Active: row.Phase != "done" && row.Phase != "review"}
	if row.Phase == "review" {
		out.Message = "结果待核对，请凭订单号联系客服"
		out.CanCancel = false
		out.CanFinish = false
	}
	if row.RetailRefundState == "pending" {
		out.Message = "供货退款已确认，正在退还本次实际支付金额"
	}
	if len(row.SnapshotCipher) > 0 {
		plain, e := s.svc.cipher.Open(row.SnapshotCipher, row.OrderItemID, row.SubsiteID)
		if e != nil {
			return nil, errors.InternalServer("sms.READ_FAILED", "接码信息暂时无法读取")
		}
		var snap supplyport.SMSSnapshot
		if json.Unmarshal([]byte(plain), &snap) != nil {
			return nil, errors.InternalServer("sms.READ_FAILED", "接码信息暂时无法读取")
		}
		out.PhoneNumber = snap.PhoneNumber
		out.OtpCode = snap.OTPCode
		out.OtpMessage = snap.OTPMessage
		out.ExpiresAt = snap.ExpiresAt
		out.CancelAvailableAt = snap.CancelAvailableAt
	}
	op, e := data.Client(ctx, s.svc.repo.data).SMSOperation.Query().Where(so.IntentID(row.ID)).Order(ent.Desc(so.FieldID)).First(ctx)
	if e != nil && !ent.IsNotFound(e) {
		return nil, e
	}
	if op != nil {
		out.OperationStatus = op.Status
		out.OperationAction = op.Action
		if strings.HasPrefix(op.OperationID, "smsop_") {
			out.OperationRequestId = strings.TrimPrefix(op.OperationID, "smsop_")
		}
		if op.Status == "pending" || op.Status == "review" {
			out.CanCancel = false
			out.CanFinish = false
		}
		if op.Status == "review" {
			out.Message = "上一次操作结果待核对，请联系客服"
		}
		if op.Status == "rejected" {
			out.Message = "上一次操作未获允许，可继续查看短信或按当前能力操作"
		}
	}
	if row.RetailRefundState == "pending" || row.RetailRefundState == "succeeded" {
		out.OperationStatus, out.OperationAction = "", ""
		out.Message = ""
		if row.RetailRefundState == "pending" {
			out.Message = "供货退款已确认，正在退还本次实际支付金额"
		}
	}
	if row.Phase == "done" || row.Phase == "refund" {
		out.CanCancel = false
		out.CanFinish = false
	}
	return out, nil
}
func (s *StoreSMSService) ActSMS(ctx context.Context, req *storefrontv1.SMSActionRequest) (*storefrontv1.SMSOrderReply, error) {
	row, e := s.owned(ctx, req.OrderNo)
	if e != nil {
		return nil, e
	}
	if req.Action != "cancel" && req.Action != "finish" {
		return nil, errors.BadRequest("sms.ACTION", "不支持的接码操作")
	}
	// Browser-generated idempotency key is validated; it is never an arbitrary action URL.
	if _, e := uuid.Parse(req.RequestId); e != nil {
		return nil, errors.BadRequest("sms.REQUEST_ID", "请刷新后重试")
	}
	operationID := "smsop_" + req.RequestId
	e = data.Tx(ctx, s.svc.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.svc.repo.data)
		n, e := c.SMSIntent.Update().Where(si.ID(row.ID), si.LeaseUntilLTE(time.Now().Unix())).SetNextRunAt(0).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.Conflict("sms.BUSY", "状态同步中，请稍后重试")
		}
		current, e := c.SMSIntent.Get(ctx, row.ID)
		if e != nil {
			return e
		}
		previous, e := c.SMSOperation.Query().Where(so.OperationID(operationID)).Only(ctx)
		if e == nil {
			if previous.IntentID != row.ID || previous.Action != req.Action {
				return errors.Conflict("sms.CONFLICT", "操作标识冲突")
			}
			return nil
		}
		if !ent.IsNotFound(e) {
			return e
		}
		pending, e := c.SMSOperation.Query().Where(so.IntentID(row.ID), so.StatusIn("pending", "review")).Exist(ctx)
		if e != nil {
			return e
		}
		if pending {
			return errors.Conflict("sms.PENDING", "已有操作待确认")
		}
		if current.Phase != "query" || req.Action == "cancel" && !current.CanCancel || req.Action == "finish" && !current.CanFinish {
			return errors.BadRequest("sms.UNAVAILABLE", "当前状态不允许该操作")
		}
		return c.SMSOperation.Create().SetIntentID(row.ID).SetSubsiteID(row.SubsiteID).SetOperationID(operationID).SetAction(req.Action).Exec(ctx)
	})
	if e != nil {
		return nil, e
	}
	row, e = s.owned(ctx, req.OrderNo)
	if e != nil {
		return nil, e
	}
	return s.reply(ctx, row)
}
func (s *AdminProcurementService) ListSMS(ctx context.Context, req *adminv1.ListProcurementsRequest) (*adminv1.SMSDiagnostics, error) {
	c := data.Client(ctx, s.repo.data)
	q := c.SMSIntent.Query().Where(si.SubsiteID(tenancy.FromContext(ctx).SubsiteID))
	if req.Status != "" {
		q.Where(si.Phase(req.Status))
	}
	if req.OrderNo != "" {
		o, e := c.Order.Query().Where(order.OrderNo(req.OrderNo), order.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Only(ctx)
		if ent.IsNotFound(e) {
			return &adminv1.SMSDiagnostics{}, nil
		}
		if e != nil {
			return nil, e
		}
		q.Where(si.OrderID(o.ID))
	}
	total, e := q.Clone().Count(ctx)
	if e != nil {
		return nil, e
	}
	p, size := procurePageParams(req.Page, req.PageSize)
	rows, e := q.Order(ent.Desc(si.FieldID)).Offset((p - 1) * size).Limit(size).All(ctx)
	if e != nil {
		return nil, e
	}
	out := &adminv1.SMSDiagnostics{Total: int64(total), SalesEnabled: data.SMSSalesEnabled()}
	for _, r := range rows {
		o, e := c.Order.Get(ctx, r.OrderID)
		if e != nil {
			return nil, e
		}
		out.Items = append(out.Items, &adminv1.SMSDiagnostic{Id: r.ID, OrderNo: o.OrderNo, ConnectionId: r.ConnectionID, Phase: r.Phase, State: r.State, RefundStatus: r.RetailRefundState, LastError: r.LastError, Attempts: int32(r.Attempts), UpdatedAt: r.UpdatedAt.Unix()})
	}
	return out, nil
}
func (s *AdminProcurementService) RetrySMS(ctx context.Context, req *adminv1.RetrySMSRequest) (*emptypb.Empty, error) {
	if len([]rune(req.Reason)) < 3 || len([]rune(req.Reason)) > 180 {
		return nil, errors.BadRequest("sms.REASON", "请填写核对和重试原因（3–180 字）")
	}
	e := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		r, e := c.SMSIntent.Query().Where(si.ID(req.Id), si.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Only(ctx)
		if e != nil {
			return e
		}
		if r.Phase == "done" {
			return errors.BadRequest("sms.DONE", "任务已经完成")
		}
		phase := "purchase"
		if r.UpstreamOrderID != nil {
			phase = "query"
		}
		if data.SMSRefundConfirmed(r) {
			phase = "refund"
		}
		n, e := c.SMSIntent.Update().Where(si.ID(r.ID), si.Phase(r.Phase), si.Version(r.Version), si.Attempts(r.Attempts), si.RetailRefundState(r.RetailRefundState), si.LeaseToken(r.LeaseToken), si.LeaseUntilLTE(time.Now().Unix())).SetPhase(phase).SetAttempts(0).SetLastError("").SetNextRunAt(0).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.Conflict("sms.BUSY", "任务正在同步")
		}
		if e := c.SMSOperation.Update().Where(so.IntentID(r.ID), so.Status("review")).SetStatus("pending").SetAttempts(0).Exec(ctx); e != nil {
			return e
		}
		actor := uint64(0)
		if claims := identity.ClaimsFromContext(ctx); claims != nil {
			actor = claims.Subject
		}
		return c.OrderStatusEvent.Create().SetOrderID(r.OrderID).SetFromStatus("sms_review").SetToStatus("sms_retry").SetEvent("sms_retry").SetOperator("admin").SetOperatorID(actor).SetReason(fmt.Sprintf("恢复原接码意图：%s", req.Reason)).Exec(ctx)
	})
	return &emptypb.Empty{}, e
}
