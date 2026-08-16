package payment

// 支付服务（P1-04；admin 渠道/支付单/退款 + storefront 创建支付 + 回调入口）。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"

	"github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ── Admin ──

// AdminPaymentService 管理面支付服务。
type AdminPaymentService struct {
	adminv1.UnimplementedAdminPaymentServiceServer
	repo *PaymentRepoImpl
	data *data.Data
}

// NewAdminPaymentService 构造。
func NewAdminPaymentService(repo *PaymentRepoImpl, d *data.Data) *AdminPaymentService {
	return &AdminPaymentService{repo: repo, data: d}
}

// ListChannels 渠道列表（凭据脱敏）。
func (s *AdminPaymentService) ListChannels(ctx context.Context, _ *emptypb.Empty) (*adminv1.ChannelList, error) {
	rows, err := s.repo.ListChannels(ctx)
	if err != nil {
		return nil, errors.InternalServer("payment.LIST_FAILED", "读取渠道失败")
	}
	reply := &adminv1.ChannelList{}
	for _, ch := range rows {
		reply.Channels = append(reply.Channels, ToChannelPB(ch))
	}
	return reply, nil
}

// CreateChannel 创建渠道。
func (s *AdminPaymentService) CreateChannel(ctx context.Context, req *adminv1.CreateChannelRequest) (*adminv1.Channel, error) {
	if req.GetName() == "" || req.GetCode() == "" || req.GetDriver() == "" {
		return nil, errors.BadRequest("payment.INVALID_INPUT", "名称/编码/驱动必填")
	}
	ch, err := s.repo.CreateChannel(ctx, req.GetName(), req.GetCode(), req.GetDriver(),
		req.GetConfigJson(), req.GetFee(), req.GetFeeType(), req.GetEnabled(), req.GetSort())
	if err != nil {
		return nil, errors.InternalServer("payment.CREATE_FAILED", "创建失败（code 可能重复）")
	}
	return ToChannelPB(ch), nil
}

// UpdateChannel 更新渠道。
func (s *AdminPaymentService) UpdateChannel(ctx context.Context, req *adminv1.UpdateChannelRequest) (*adminv1.Channel, error) {
	ch, err := s.repo.UpdateChannel(ctx, req.GetId(), req.GetName(), req.GetConfigJson(),
		req.GetFee(), req.GetEnabled(), req.GetSort())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("payment.CHANNEL_NOT_FOUND", "渠道不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("payment.UPDATE_FAILED", "更新失败")
	}
	return ToChannelPB(ch), nil
}

// DeleteChannel 删除渠道。
func (s *AdminPaymentService) DeleteChannel(ctx context.Context, req *adminv1.DeleteChannelRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteChannel(ctx, req.GetId()); err != nil {
		return nil, errors.NotFound("payment.CHANNEL_NOT_FOUND", "渠道不存在")
	}
	return &emptypb.Empty{}, nil
}

// ListPayments 支付单列表。
func (s *AdminPaymentService) ListPayments(ctx context.Context, req *adminv1.ListPaymentsRequest) (*adminv1.ListPaymentsReply, error) {
	limit := req.GetLimit()
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.repo.ListPayments(ctx, req.GetStatus(), req.GetOrderNo(), req.GetCursor(), limit)
	if err != nil {
		return nil, errors.InternalServer("payment.LIST_FAILED", "读取支付单失败")
	}
	reply := &adminv1.ListPaymentsReply{}
	for _, p := range rows {
		orderNo := s.getOrderNo(ctx, p.OrderID)
		reply.Payments = append(reply.Payments, ToPaymentPB(p, orderNo))
	}
	if len(rows) == int(limit) {
		reply.NextCursor = rows[len(rows)-1].ID
	}
	return reply, nil
}

// GetPayment 支付单详情。
func (s *AdminPaymentService) GetPayment(ctx context.Context, req *adminv1.GetPaymentRequest) (*adminv1.Payment, error) {
	p, err := s.repo.GetPayment(ctx, req.GetId())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("payment.NOT_FOUND", "支付单不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("payment.GET_FAILED", "查询失败")
	}
	return ToPaymentPB(p, s.getOrderNo(ctx, p.OrderID)), nil
}

// CapturePayment 补单（M1a 框架——真实渠道拉取 M1b 接入）。
func (s *AdminPaymentService) CapturePayment(ctx context.Context, req *adminv1.CapturePaymentRequest) (*adminv1.Payment, error) {
	p, err := s.repo.GetPayment(ctx, req.GetId())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("payment.NOT_FOUND", "支付单不存在")
	}
	if err != nil {
		return nil, err
	}
	// M1a：直接标记成功（wallet 渠道）；真实渠道 M1b 接 Capturer
	fact := CallbackFact{
		Channel: p.Channel, Amount: p.Amount, Currency: "CNY", Success: true,
		ChannelOrderNo: fmt.Sprintf("manual-%d", p.ID),
	}
	if err := s.repo.HandleCallback(ctx, p.ID, fact); err != nil {
		return nil, errors.InternalServer("payment.CAPTURE_FAILED", "补单失败: "+err.Error())
	}
	updated, _ := s.repo.GetPayment(ctx, req.GetId())
	return ToPaymentPB(updated, s.getOrderNo(ctx, p.OrderID)), nil
}

// CreateRefund 创建退款单。
func (s *AdminPaymentService) CreateRefund(ctx context.Context, req *adminv1.CreateRefundRequest) (*adminv1.RefundOrder, error) {
	o, err := data.Client(ctx, s.data).Order.Query().Where(order.OrderNo(req.GetOrderNo())).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
	}
	if err != nil {
		return nil, err
	}
	rf, err := s.repo.CreateRefund(ctx, o.ID, req.GetAmountCents(), req.GetChannel(), req.GetReason())
	if err != nil {
		return nil, errors.InternalServer("payment.REFUND_FAILED", "创建退款失败")
	}
	return ToRefundPB(rf, o.OrderNo), nil
}

// ListRefunds 退款列表。
func (s *AdminPaymentService) ListRefunds(ctx context.Context, req *adminv1.ListRefundsRequest) (*adminv1.ListRefundsReply, error) {
	rows, err := s.repo.ListRefunds(ctx, req.GetStatus())
	if err != nil {
		return nil, errors.InternalServer("payment.LIST_FAILED", "读取退款失败")
	}
	reply := &adminv1.ListRefundsReply{}
	for _, rf := range rows {
		reply.Refunds = append(reply.Refunds, ToRefundPB(rf, s.getOrderNo(ctx, rf.OrderID)))
	}
	return reply, nil
}

func (s *AdminPaymentService) getOrderNo(ctx context.Context, orderID uint64) string {
	o, err := data.Client(ctx, s.data).Order.Get(ctx, orderID)
	if err != nil {
		return ""
	}
	return o.OrderNo
}

// ── Storefront ──

// StorePaymentService 顾客支付服务。
type StorePaymentService struct {
	storefrontv1.UnimplementedStorePaymentServiceServer
	repo *PaymentRepoImpl
	data *data.Data
}

// NewStorePaymentService 构造。
func NewStorePaymentService(repo *PaymentRepoImpl, d *data.Data) *StorePaymentService {
	return &StorePaymentService{repo: repo, data: d}
}

// CreatePayment 创建支付。
func (s *StorePaymentService) CreatePayment(ctx context.Context, req *storefrontv1.CreatePaymentRequest) (*storefrontv1.CreatePaymentReply, error) {
	if req.GetOrderNo() == "" || req.GetChannel() == "" {
		return nil, errors.BadRequest("payment.INVALID_INPUT", "订单号与渠道必填")
	}
	client := data.Client(ctx, s.data)

	// 查订单
	o, err := client.Order.Query().Where(order.OrderNo(req.GetOrderNo())).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
	}
	if err != nil {
		return nil, err
	}
	if o.Status != order.StatusPendingPayment {
		return nil, errors.BadRequest("payment.ORDER_NOT_PENDING", "订单不在待支付状态")
	}

	// 检查渠道启用
	ch, err := client.PaymentChannel.Query().
		Where(paymentchannel.Code(req.GetChannel()), paymentchannel.Enabled(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("payment.CHANNEL_NOT_FOUND", "支付渠道不存在或未启用")
	}
	if err != nil {
		return nil, err
	}

	// wallet 渠道：余额支付（直接 markPaid——M1 接 wallet.DebitInTx）
	if ch.Driver == "wallet" {
		p, err := s.repo.CreatePayment(ctx, o.ID, ch.Code, o.TotalAmount, "")
		if err != nil {
			return nil, errors.InternalServer("payment.CREATE_FAILED", "创建支付失败")
		}
		// 直接标记成功
		fact := CallbackFact{
			Channel: ch.Code, Amount: o.TotalAmount, Currency: "CNY",
			Success: true, ChannelOrderNo: fmt.Sprintf("wallet-%d", p.ID),
		}
		if err := s.repo.HandleCallback(ctx, p.ID, fact); err != nil {
			return nil, errors.InternalServer("payment.WALLET_FAILED", "余额支付失败: "+err.Error())
		}
		return &storefrontv1.CreatePaymentReply{
			PaymentId: p.ID, Type: "redirect", Payload: "/order/success?no=" + o.OrderNo,
		}, nil
	}

	// 外部渠道：创建 pending 支付单，返回跳转信息（M1b 接真实适配器）
	p, err := s.repo.CreatePayment(ctx, o.ID, ch.Code, o.TotalAmount, "")
	if err != nil {
		return nil, errors.InternalServer("payment.CREATE_FAILED", "创建支付失败")
	}
	return &storefrontv1.CreatePaymentReply{
		PaymentId: p.ID, Type: "redirect",
		Payload: fmt.Sprintf("/payment/pending?id=%d&channel=%s", p.ID, ch.Code),
	}, nil
}

// ── 回调路由（免鉴权——架构测试规则 9）────────────────────

// RegisterPaymentCallback 支付回调入口（POST /payments/callback/{provider}）。
// 不挂 JWT；验签由各渠道适配器完成（M1b 接入真实渠道后生效）。
func RegisterPaymentCallback(srv *khttp.Server, repo *PaymentRepoImpl, d *data.Data) {
	srv.Route("/payments").POST("/callback/{provider}", func(ctx khttp.Context) error {
		provider := ctx.Vars().Get("provider")

		// 读取 raw body（上限 1MB）
		body := make([]byte, 0)
		if ctx.Request().Body != nil {
			b, err := io.ReadAll(io.LimitReader(ctx.Request().Body, 1<<20))
			if err != nil {
				return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "body read failed"})
			}
			body = b
		}

		// M1a 框架：wallet 渠道直接处理；外部渠道 M1b 接适配器验签
		var req struct {
			PaymentID      uint64 `json:"payment_id"`
			OrderNo        string `json:"order_no"`
			Amount         int64  `json:"amount"`
			ChannelOrderNo string `json:"channel_order_no"`
			Success        bool   `json:"success"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "invalid json"})
		}

		if !req.Success {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored"})
		}

		// 查支付单
		var paymentID uint64
		if req.PaymentID > 0 {
			paymentID = req.PaymentID
		} else if req.OrderNo != "" {
			o, err := data.Client(ctx, d).Order.Query().Where(order.OrderNo(req.OrderNo)).Only(ctx)
			if err != nil {
				return ctx.JSON(http.StatusNotFound, map[string]string{"error": "order not found"})
			}
			p, err := data.Client(ctx, d).Payment.Query().
				Where(payment.OrderID(o.ID)).Only(ctx)
			if err != nil {
				return ctx.JSON(http.StatusNotFound, map[string]string{"error": "payment not found"})
			}
			paymentID = p.ID
		}
		if paymentID == 0 {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "payment_id or order_no required"})
		}

		// 走回调管线
		fact := CallbackFact{
			Channel: provider, Amount: req.Amount, Currency: "CNY",
			Success: true, ChannelOrderNo: req.ChannelOrderNo,
		}
		if err := repo.HandleCallback(ctx, paymentID, fact); err != nil {
			// ACK 策略：系统错误 500 触发渠道重试
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
}

var _ = refundorder.ChannelWallet // 保持引用
