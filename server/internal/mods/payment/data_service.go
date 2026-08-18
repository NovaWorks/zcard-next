package payment

// 支付服务（P1-04；admin 渠道/支付单/退款 + storefront 创建支付 + 回调入口）。

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

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
	// 真实渠道：走 Capturer 主动查单；不支持 Capturer 的渠道（wallet）保持直接标记成功
	var fact CallbackFact
	provider, perr := s.repo.reg.Provider(p.Channel)
	if perr == nil {
		if capr, ok := provider.(port.Capturer); ok {
			ch, _ := data.Client(ctx, s.data).PaymentChannel.Query().Where(paymentchannel.Code(p.Channel)).Only(ctx)
			f, err := capr.QueryPayment(ctx, p.ChannelOrderNo, s.repo.DecryptConfig(ch))
			if err != nil {
				return nil, errors.InternalServer("payment.CAPTURE_FAILED", "查单失败: "+err.Error())
			}
			if !f.Success {
				return nil, errors.BadRequest("payment.NOT_PAID", "上游未支付，无法补单")
			}
			fact = CallbackFact{Channel: p.Channel, Amount: int64(f.Amount), Currency: f.Currency, Success: true, ChannelOrderNo: f.ChannelOrderNo}
		} else {
			fact = CallbackFact{Channel: p.Channel, Amount: p.Amount, Currency: "CNY", Success: true, ChannelOrderNo: fmt.Sprintf("manual-%d", p.ID)}
		}
	} else {
		fact = CallbackFact{Channel: p.Channel, Amount: p.Amount, Currency: "CNY", Success: true, ChannelOrderNo: fmt.Sprintf("manual-%d", p.ID)}
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

	// 外部渠道：走 adapter 创建支付（返回收银台/二维码/参数包）
	provider, err := s.repo.reg.Provider(ch.Driver)
	if err != nil {
		return nil, errors.BadRequest("payment.CHANNEL_UNSUPPORTED", "渠道驱动未实现: "+ch.Driver)
	}
	cfg := s.repo.DecryptConfig(ch)
	if err := provider.ValidateConfig(cfg); err != nil {
		return nil, errors.InternalServer("payment.CHANNEL_CONFIG_INVALID", "渠道配置无效: "+err.Error())
	}
	p, err := s.repo.CreatePayment(ctx, o.ID, ch.Code, o.TotalAmount, "")
	if err != nil {
		return nil, errors.InternalServer("payment.CREATE_FAILED", "创建支付失败")
	}
	// 币种快照（P2-09 T2）：target_currency → currency 表换算 → 适配器收渠道金额
	snap := s.repo.computeCharge(ctx, cfg, money.Cents(o.TotalAmount))
	info, err := provider.CreatePayment(ctx, port.CreatePaymentRequest{
		OrderNo:       o.OrderNo,
		Channel:       ch.Code,
		Amount:        money.Cents(o.TotalAmount),
		Subject:       "订单 " + o.OrderNo,
		ReturnURL:     "",
		NotifyBaseURL: "/payments/callback/" + ch.Code,
		Config:        cfg,
		ChargedUnits:  snap.Units, ChargedCurrency: snap.Currency,
	})
	if err != nil {
		return nil, errors.InternalServer("payment.CREATE_FAILED", "发起支付失败: "+err.Error())
	}
	s.repo.snapshotCharge(ctx, p.ID, snap)
	return &storefrontv1.CreatePaymentReply{
		PaymentId: p.ID, Type: info.Type, Payload: string(info.Payload),
	}, nil
}

// ── 回调路由（免鉴权——架构测试规则 9）────────────────────

// RegisterPaymentCallback 支付回调入口（POST /payments/callback/{channel_code}）
// + PayPal return 同步捕获（GET /payments/return/{channel}?token=<order_id>）。
// 均不挂 JWT；验签由各渠道适配器完成。ACK 语义：验签失败 401，状态冲突 200，
// 系统错误 500 触发重试。
func RegisterPaymentCallback(srv *khttp.Server, repo *PaymentRepoImpl, d *data.Data) {
	payments := srv.Route("/payments")
	payments.POST("/callback/{channel}", func(ctx khttp.Context) error {
		channelCode := ctx.Vars().Get("channel")
		r := ctx.Request()

		// 1) 读 body（上限 1MB）
		body := make([]byte, 0)
		if r.Body != nil {
			b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "body read failed"})
			}
			body = b
		}

		// 2) 查渠道（按 code；停用渠道拒绝）
		ch, err := data.Client(ctx, d).PaymentChannel.Query().
			Where(paymentchannel.Code(channelCode), paymentchannel.Enabled(true)).Only(ctx)
		if ent.IsNotFound(err) {
			return ctx.JSON(http.StatusNotFound, map[string]string{"error": "channel not found"})
		}
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		// 3) 取 adapter + 解密凭据
		provider, err := repo.reg.Provider(ch.Driver)
		if err != nil {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "unsupported driver"})
		}
		cfg := repo.DecryptConfig(ch)

		// 4/5) 解析 + 验签 → CallbackFact：
		//     Webhooker 分支（stripe/paypal——JSON body + 签名头，SDK 验签需凭据）
		//     优先于表单分支（alipay/wechat/epay/epusdt）
		var f *port.CallbackFact
		if hooker, ok := provider.(port.Webhooker); ok && isWebhookRequest(r, ch.Driver) {
			headers := map[string]string{}
			for k, vv := range r.Header {
				if len(vv) > 0 {
					headers[k] = vv[0]
				}
			}
			fact, err := hooker.ParseWebhook(headers, body, cfg)
			if err != nil {
				return ctx.JSON(http.StatusUnauthorized, map[string]string{"error": "verify failed"})
			}
			f = fact
		} else {
			verifier, ok := provider.(port.CallbackVerifier)
			if !ok {
				return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "driver has no callback verifier"})
			}
			form, err := parseCallbackForm(r, body)
			if err != nil {
				return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "parse failed"})
			}
			f, err = verifier.VerifyCallback(form, cfg)
			if err != nil {
				return ctx.JSON(http.StatusUnauthorized, map[string]string{"error": "verify failed"})
			}
		}
		if !f.Success {
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ignored"})
		}

		// 6) 定位支付单（订单号定位；充值单 RCH<id> 前缀走 recharge 关联）
		paymentID := locatePaymentByFact(ctx, d, channelCode, f)
		if paymentID == 0 {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "payment not found"})
		}

		// 7) 走回调管线（四重校验 + 幂等 + markPaid）
		fact := CallbackFact{
			Channel:        channelCode,
			ChannelOrderNo: f.ChannelOrderNo,
			OrderNo:        f.OrderNo,
			Amount:         int64(f.Amount),
			Currency:       f.Currency,
			Success:        f.Success,
			Raw:            f.Raw,
		}
		if err := repo.HandleCallback(ctx, paymentID, fact); err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		// 应答体渠道感知（Acker 能力位）：epusdt 类网关要求纯文本 "ok"；
		// 未实现 Acker 的渠道维持 JSON（alipay/wechat/epay 现状）
		if acker, ok := provider.(port.Acker); ok {
			return ctx.String(http.StatusOK, acker.SuccessAck())
		}
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	// PayPal return 同步捕获（P2-09 T4）：买家在 PayPal 授权后跳回
	// return_url（PayPal 追加 token=<order_id>，1.x 生产依赖）——
	// 先查后捕（Capturer），成功后走回调管线 markPaid，302 回店铺页。
	payments.GET("/return/{channel}", func(ctx khttp.Context) error {
		channelCode := ctx.Vars().Get("channel")
		token := strings.TrimSpace(ctx.Request().URL.Query().Get("token"))
		// 单号白名单（匿名端点出站放大器——1.x M-11）
		if !paypalOrderIDTokenRe.MatchString(token) {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "invalid token"})
		}
		ch, err := data.Client(ctx, d).PaymentChannel.Query().
			Where(paymentchannel.Code(channelCode), paymentchannel.Enabled(true)).Only(ctx)
		if ent.IsNotFound(err) {
			return ctx.JSON(http.StatusNotFound, map[string]string{"error": "channel not found"})
		}
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		provider, err := repo.reg.Provider(ch.Driver)
		if err != nil {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "unsupported driver"})
		}
		capturer, ok := provider.(port.Capturer)
		if !ok {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "driver has no capturer"})
		}
		f, err := capturer.QueryPayment(ctx, token, repo.DecryptConfig(ch))
		if err != nil {
			return ctx.JSON(http.StatusBadGateway, map[string]string{"error": "capture failed"})
		}
		// 未支付/处理中：302 回支付页（可重试或换渠道）
		fallback := "/payment/" + f.OrderNo
		if strings.HasPrefix(f.OrderNo, "RCH") {
			fallback = "/member"
		}
		if !f.Success {
			return ctx.JSON(http.StatusFound, khttp.NewRedirect(fallback, http.StatusFound))
		}
		paymentID := locatePaymentByFact(ctx, d, channelCode, f)
		if paymentID == 0 {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "payment not found"})
		}
		if err := repo.HandleCallback(ctx, paymentID, CallbackFact{
			Channel:        channelCode,
			ChannelOrderNo: f.ChannelOrderNo,
			OrderNo:        f.OrderNo,
			Amount:         int64(f.Amount),
			Currency:       f.Currency,
			Success:        f.Success,
			Raw:            f.Raw,
		}); err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return ctx.JSON(http.StatusFound, khttp.NewRedirect(fallback, http.StatusFound))
	})
}

// locatePaymentByFact 按回调事实定位支付单（订单号定位；充值单 RCH<id> 前缀走
// recharge 关联）。回调路由与 PayPal return 捕获端点共用。
func locatePaymentByFact(ctx context.Context, d *data.Data, channelCode string, f *port.CallbackFact) uint64 {
	if f == nil || f.OrderNo == "" {
		return 0
	}
	client := data.Client(ctx, d)
	if rid, ok := strings.CutPrefix(f.OrderNo, "RCH"); ok {
		if id, err := strconv.ParseUint(rid, 10, 64); err == nil {
			p, err := client.Payment.Query().
				Where(payment.RechargeOrderID(id), payment.Channel(channelCode)).Only(ctx)
			if err == nil {
				return p.ID
			}
		}
		return 0
	}
	o, err := client.Order.Query().Where(order.OrderNo(f.OrderNo)).Only(ctx)
	if err != nil {
		return 0
	}
	p, err := client.Payment.Query().
		Where(payment.OrderID(o.ID), payment.Channel(channelCode)).Only(ctx)
	if err != nil {
		return 0
	}
	return p.ID
}

// parseCallbackForm 解析回调为扁平 map：XML（wechat）→ 元素名→文本；否则表单/查询串。
func parseCallbackForm(r *http.Request, body []byte) (map[string]string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "xml") {
		return parseXMLMap(body)
	}
	_ = r.ParseForm()
	m := map[string]string{}
	for k := range r.Form {
		m[k] = r.Form.Get(k)
	}
	// JSON 回调体（epusdt 类）：UseNumber 保留原始字面——10.00 不得塌成 10
	//（验签按原文重算，签名不变式 §5.5）
	if len(m) == 0 && len(body) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		var j map[string]any
		if dec.Decode(&j) == nil {
			for k, v := range j {
				switch tv := v.(type) {
				case json.Number:
					m[k] = tv.String()
				case string:
					m[k] = tv
				case bool:
					m[k] = fmt.Sprintf("%v", tv)
				default:
					_ = tv
					if b, err := json.Marshal(v); err == nil {
						m[k] = string(b)
					}
				}
			}
		}
	}
	return m, nil
}

// isWebhookRequest webhook 分支判定：JSON 内容或渠道专属签名头存在。
// （stripe：Stripe-Signature；paypal：Paypal-Transmission-*——均为表单分支不可达的头）
func isWebhookRequest(r *http.Request, driver string) bool {
	if strings.Contains(r.Header.Get("Content-Type"), "json") {
		return true
	}
	return r.Header.Get("Stripe-Signature") != "" ||
		r.Header.Get("Paypal-Transmission-Id") != ""
}

// paypalOrderIDTokenRe PayPal return 捕获 token 白名单（匿名端点出站放大器——1.x M-11）。
var paypalOrderIDTokenRe = regexp.MustCompile(`^[A-Z0-9]{5,30}$`)

// parseXMLMap 扁平 XML（wechat 回调 <xml><k>v</k>...</xml>）→ map。
func parseXMLMap(body []byte) (map[string]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	m := map[string]string{}
	var cur string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			cur = t.Name.Local
		case xml.CharData:
			if cur != "" {
				m[cur] = string(t)
			}
		case xml.EndElement:
			cur = ""
		}
	}
	return m, nil
}

var _ = refundorder.ChannelWallet // 保持引用
