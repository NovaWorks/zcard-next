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
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
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
		reply.Channels = append(reply.Channels, s.channelPB(ctx, ch))
	}
	return reply, nil
}

// CreateChannel 创建渠道（P2-09 T5：驱动存在性 + 凭据即时校验——创建即反馈）。
func (s *AdminPaymentService) CreateChannel(ctx context.Context, req *adminv1.CreateChannelRequest) (*adminv1.Channel, error) {
	if req.GetName() == "" || req.GetCode() == "" || req.GetDriver() == "" {
		return nil, errors.BadRequest("payment.INVALID_INPUT", "名称/编码/驱动必填")
	}
	if req.GetDriver() != "wallet" { // wallet 为内置驱动（service 层直接 markPaid）
		provider, err := s.repo.reg.Provider(req.GetDriver())
		if err != nil {
			return nil, errors.BadRequest("payment.DRIVER_UNSUPPORTED", "渠道驱动未实现: "+req.GetDriver())
		}
		// 创建允许空凭据（待配置状态——勾选式添加后引导补凭据）；
		// 仅提交了实际配置时校验，填写支付信息保存那一步即时反馈
		if strings.TrimSpace(req.GetConfigJson()) != "" && req.GetConfigJson() != "{}" {
			if err := provider.ValidateConfig(json.RawMessage(req.GetConfigJson())); err != nil {
				return nil, errors.BadRequest("payment.CHANNEL_CONFIG_INVALID", err.Error())
			}
		}
	}
	feeType := req.GetFeeType()
	if feeType == "" {
		feeType = string(paymentchannel.FeeTypeFixed) // ent enum 拒绝空值——默认固定费率
	}
	methods, err := methodsJSON(req.GetMethodsJson())
	if err != nil {
		return nil, errors.BadRequest("payment.METHODS_INVALID", "支付方式列表格式错误")
	}
	ch, err := s.repo.CreateChannel(ctx, req.GetName(), req.GetCode(), req.GetDriver(),
		req.GetConfigJson(), req.GetFee(), feeType, req.GetEnabled(), req.GetSort(),
		req.GetIcon(), methods)
	if err != nil {
		return nil, errors.InternalServer("payment.CREATE_FAILED", "创建失败（code 可能重复）")
	}
	return s.channelPB(ctx, ch), nil
}

// ListDrivers 驱动元数据（P2-09 T5：admin 配置面动态表单渲染的数据源）。
func (s *AdminPaymentService) ListDrivers(ctx context.Context, _ *emptypb.Empty) (*adminv1.DriverList, error) {
	drivers := []*adminv1.Driver{{
		Code: "wallet", Name: "余额支付", Icon: "wallet",
		Description: "站内余额支付（无需外部凭据）",
	}}
	for _, p := range s.repo.reg.All() {
		d := &adminv1.Driver{Code: p.Type()}
		if m, ok := p.(port.MetaProvider); ok {
			meta := m.Meta()
			d.Name, d.Icon, d.Description = meta.Name, meta.Icon, meta.Description
		} else {
			d.Name = p.Type() // 未声明元数据的驱动回落驱动码
		}
		if f, ok := p.(port.FieldProvider); ok {
			for _, cf := range f.ConfigFields() {
				fd := &adminv1.ConfigField{
					Key: cf.Key, Label: cf.Label, Type: cf.Type, Required: cf.Required,
					Placeholder: cf.Placeholder, Help: cf.Help, Sensitive: cf.Sensitive,
					Dynamic: cf.Dynamic, Multiple: cf.Multiple, Default: cf.Default,
				}
				for _, o := range cf.Options {
					fd.Options = append(fd.Options, &adminv1.Option{Label: o.Label, Value: o.Value})
				}
				d.Fields = append(d.Fields, fd)
			}
		} else {
			// 未声明 schema 的驱动（自定义）——默认 JSON 文本框
			d.Fields = append(d.Fields, &adminv1.ConfigField{
				Key: "config", Label: "凭据 JSON", Type: "textarea", Required: true,
				Help: "该驱动未声明字段模板，直接填写 JSON",
			})
		}
		drivers = append(drivers, d)
	}
	return &adminv1.DriverList{Drivers: drivers}, nil
}

// FieldOptions 驱动字段动态选项（P2-09 T5 修复：epusdt network/token 以网关
// supported_assets 为准——转发适配器 OptionProvider，失败回落静态矩阵）。
func (s *AdminPaymentService) FieldOptions(ctx context.Context, req *adminv1.FieldOptionsRequest) (*adminv1.FieldOptionsReply, error) {
	if req.GetCode() == "" || req.GetField() == "" {
		return nil, errors.BadRequest("payment.INVALID_INPUT", "code/field 必填")
	}
	provider, err := s.repo.reg.Provider(req.GetCode())
	if err != nil {
		return nil, errors.BadRequest("payment.DRIVER_UNSUPPORTED", "渠道驱动未实现: "+req.GetCode())
	}
	op, ok := provider.(port.OptionProvider)
	if !ok {
		return nil, errors.BadRequest("payment.OPTIONS_UNSUPPORTED", "驱动不支持动态选项")
	}
	res, err := op.FieldOptions(ctx, req.GetField(), json.RawMessage(req.GetConfigJson()))
	if err != nil {
		return nil, errors.InternalServer("payment.OPTIONS_FAILED", "加载选项失败: "+err.Error())
	}
	reply := &adminv1.FieldOptionsReply{Fallback: res.Fallback}
	for _, o := range res.Options {
		reply.Options = append(reply.Options, &adminv1.Option{Label: o.Label, Value: o.Value})
	}
	return reply, nil
}

// channelPB 渠道协议对象（P2-09 T5）：
// 补充已配置字段名 + 回调地址；凭据脱敏回显——敏感字段掩码 ****
// （编辑体验：非敏感字段可回显，敏感字段留空不覆盖）。
func (s *AdminPaymentService) channelPB(ctx context.Context, ch *ent.PaymentChannel) *adminv1.Channel {
	pb := ToChannelPB(ch)
	pb.ConfiguredFields = s.repo.ConfiguredFields(ch)
	pb.CallbackUrl = s.repo.CallbackURL(ctx, ch.Code)
	if ch.Driver != "wallet" {
		if p, err := s.repo.reg.Provider(ch.Driver); err == nil {
			if fp, ok := p.(port.FieldProvider); ok {
				sensitive := map[string]bool{}
				for _, f := range fp.ConfigFields() {
					if f.Sensitive {
						sensitive[f.Key] = true
					}
				}
				var m map[string]json.RawMessage
				if json.Unmarshal(s.repo.DecryptConfig(ch), &m) == nil {
					for k := range m {
						if sensitive[k] {
							m[k] = json.RawMessage(`"****"`)
						}
					}
					if b, err := json.Marshal(m); err == nil {
						pb.ConfigJson = string(b)
					}
				}
			}
		}
	}
	return pb
}

// UpdateChannel 更新渠道（P2-09 T5：fee_type 更新 + 凭据变更即时校验；
// config_json=**** 跳过凭据修改——敏感字段留空不覆盖）。
func (s *AdminPaymentService) UpdateChannel(ctx context.Context, req *adminv1.UpdateChannelRequest) (*adminv1.Channel, error) {
	if ft := req.GetFeeType(); ft != "" && ft != string(paymentchannel.FeeTypePercent) && ft != string(paymentchannel.FeeTypeFixed) {
		return nil, errors.BadRequest("payment.INVALID_INPUT", "fee_type 须为 percent/fixed")
	}
	if req.GetConfigJson() != "" && req.GetConfigJson() != `"****"` {
		old, err := data.Client(ctx, s.data).PaymentChannel.Get(ctx, req.GetId())
		if ent.IsNotFound(err) {
			return nil, errors.NotFound("payment.CHANNEL_NOT_FOUND", "渠道不存在")
		}
		if err != nil {
			return nil, errors.InternalServer("payment.UPDATE_FAILED", "读取渠道失败")
		}
		if old.Driver != "wallet" {
			provider, err := s.repo.reg.Provider(old.Driver)
			if err != nil {
				return nil, errors.BadRequest("payment.DRIVER_UNSUPPORTED", "渠道驱动未实现: "+old.Driver)
			}
			if err := provider.ValidateConfig(json.RawMessage(req.GetConfigJson())); err != nil {
				return nil, errors.BadRequest("payment.CHANNEL_CONFIG_INVALID", err.Error())
			}
		}
	}
	var methods []map[string]any
	if req.MethodsJson != nil { // optional：缺省=不修改
		var mErr error
		methods, mErr = methodsJSON(req.GetMethodsJson())
		if mErr != nil {
			return nil, errors.BadRequest("payment.METHODS_INVALID", "支付方式列表格式错误")
		}
	}
	ch, err := s.repo.UpdateChannel(ctx, req.GetId(), req.GetName(), req.GetConfigJson(),
		req.GetFee(), req.GetFeeType(), req.GetEnabled(), req.GetSort(),
		req.Icon != nil, req.GetIcon(), req.MethodsJson != nil, methods)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("payment.CHANNEL_NOT_FOUND", "渠道不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("payment.UPDATE_FAILED", "更新失败")
	}
	return s.channelPB(ctx, ch), nil
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

// requestBaseURL 从 kratos transport 取 scheme://host（支付回跳/回调绝对化：
// 外部网关只认绝对地址）。scheme 优先 X-Forwarded-Proto（反代 https），host 优先
// Host 回落 X-Forwarded-Host。用 RequestFromServerContext 而非断言 khttp.Context
// 巨型接口（同 affiliate requestHost 教训）。
func requestBaseURL(ctx context.Context) string {
	r, ok := khttp.RequestFromServerContext(ctx)
	if !ok {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if host == "" {
		host = r.Header.Get("X-Forwarded-Host")
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// absolutePayURL 相对支付 URL 绝对化（已绝对原样返回；无请求上下文时保持相对
// ——此时适配器回落值/渠道显式配置兜底）。
func absolutePayURL(ctx context.Context, path string) string {
	if path == "" || strings.HasPrefix(path, "http") {
		return path
	}
	if base := requestBaseURL(ctx); base != "" {
		return base + path
	}
	return path
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

// ListChannels 启用渠道列表（P2-09 T5：渠道下拉数据源——替代前端硬编码枚举）。
// 过滤：启用 + 已配置（空凭据的「待配置」渠道不对顾客展示；wallet 内置无需配置）。
// 游客不下发 wallet（余额支付需登录态；游客仅可用真实支付渠道）。
func (s *StorePaymentService) ListChannels(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.ChannelListReply, error) {
	rows, err := data.Client(ctx, s.data).PaymentChannel.Query().
		Where(paymentchannel.Enabled(true)).
		Order(ent.Asc(paymentchannel.FieldSort)).
		All(ctx)
	if err != nil {
		return nil, errors.InternalServer("payment.LIST_FAILED", "读取渠道失败")
	}
	// 游客判定：无登录 claims（余额支付依赖钱包账户）
	guest := identity.ClaimsFromContext(ctx) == nil
	reply := &storefrontv1.ChannelListReply{}
	for _, ch := range rows {
		if guest && ch.Driver == "wallet" {
			continue // 游客不可用余额支付
		}
		if ch.Driver != "wallet" && len(s.repo.ConfiguredFields(ch)) == 0 {
			continue // 待配置渠道不下发
		}
		item := &storefrontv1.ChannelItem{Code: ch.Code, Name: ch.Name, Driver: ch.Driver, Icon: ch.Icon}
		for _, m := range parseMethods(ch) {
			if !m.Enabled {
				continue
			}
			item.Methods = append(item.Methods, &storefrontv1.MethodItem{Code: m.Code, Name: m.Name, Icon: m.Icon})
		}
		reply.Channels = append(reply.Channels, item)
	}
	return reply, nil
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
		// 余额支付同步完成：payload 指向支付页（该页会呈现成功态/卡密）——
		// 曾返回 /order/success?no=（前端无此路由，弹窗即 404）
		return &storefrontv1.CreatePaymentReply{
			PaymentId: p.ID, Type: "redirect", Payload: "/payment/" + o.OrderNo,
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
	// 方式级参数（收银台顾客所选支付方式：易支付支付宝/微信、USDT 选链）：
	// 渠道配置了 methods 时 method 必填且须在启用列表内；params 透传适配器路由网关。
	var methodCode string
	var methodParams map[string]string
	if ms := parseMethods(ch); len(ms) > 0 {
		found := false
		for _, m := range ms {
			if m.Enabled && m.Code == req.GetMethod() {
				found = true
				methodCode = m.Code
				methodParams = m.Params
				break
			}
		}
		if !found {
			return nil, errors.BadRequest("payment.METHOD_INVALID", "请选择该渠道支持的支付方式")
		}
	}
	p, err := s.repo.CreatePayment(ctx, o.ID, ch.Code, o.TotalAmount, "")
	if err != nil {
		return nil, errors.InternalServer("payment.CREATE_FAILED", "创建支付失败")
	}
	// 币种快照（P2-09 T2）：target_currency → currency 表换算 → 适配器收渠道金额
	snap := s.repo.computeCharge(ctx, cfg, money.Cents(o.TotalAmount))
	// 回跳/回调绝对化（易支付/Stripe/PayPal 等外部网关只认绝对地址）：
	// notify 用 site/url 前缀（CallbackURL；未配置时请求 Host 兜底）；
	// return 回支付页（轮询出结果并展示卡密）——曾传空值，客户付完被
	// 网关回跳到空地址弹 404
	info, err := provider.CreatePayment(ctx, port.CreatePaymentRequest{
		OrderNo:       o.OrderNo,
		Channel:       ch.Code,
		Amount:        money.Cents(o.TotalAmount),
		Subject:       "订单 " + o.OrderNo,
		ReturnURL:     absolutePayURL(ctx, "/payment/"+o.OrderNo),
		NotifyBaseURL: absolutePayURL(ctx, s.repo.CallbackURL(ctx, ch.Code)),
		Config:        cfg,
		ChargedUnits:  snap.Units, ChargedCurrency: snap.Currency,
		MethodCode:    methodCode, MethodParams: methodParams,
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
	// GET+POST 双注册：易支付族网关异步通知多为 GET（query 参数），ParseForm
	// 本就合并 query/body，同一 handler 即可
	handler := func(ctx khttp.Context) error {
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
	}
	payments.POST("/callback/{channel}", handler)
	payments.GET("/callback/{channel}", handler)
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
