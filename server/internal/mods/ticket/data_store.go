package ticket

// 工单服务（）：storefront + admin 双面 + 付费加急（余额）+ 通知双侧。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/ticket"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	settingsport "github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

	"google.golang.org/protobuf/types/known/emptypb"
)

// 加急费配置键（settings.ticket.urgent_fee；0=免费直接加急）。
const urgentFeeGroup, urgentFeeKey = "ticket", "urgent_fee"

// 付费加急 SLA（小时；普通单 48h——仅回填预留位，M4 告警消费）。
const (
	urgentSLAHours = 2
	normalSLAHours = 48
)

// StoreTicketService 用户工单服务。
type StoreTicketService struct {
	storefrontv1.UnimplementedStoreTicketServiceServer
	repo     *TicketRepo
	gen      *id.Generator
	wallet   walletport.Wallet
	settings settingsport.Provider
	outbox   events.Writer
	log      *slog.Logger
}

// NewStoreTicketService 构造。
func NewStoreTicketService(repo *TicketRepo, gen *id.Generator, wallet walletport.Wallet, settings settingsport.Provider, outbox events.Writer, logger *slog.Logger) *StoreTicketService {
	return &StoreTicketService{repo: repo, gen: gen, wallet: wallet, settings: settings, outbox: outbox, log: logger}
}

// CreateTicket 创建（登录/游客）。
func (s *StoreTicketService) CreateTicket(ctx context.Context, req *storefrontv1.CreateTicketRequest) (*storefrontv1.Ticket, error) {
	if req.GetContent() == "" {
		return nil, errors.New("ticket.CONTENT_REQUIRED")
	}
	if req.GetType() != "presale" && req.GetType() != "aftersale" {
		return nil, errors.New("ticket.TYPE_INVALID")
	}
	userID := currentUserID(ctx)
	if userID == 0 && req.GetGuestContact() == "" {
		return nil, ErrGuestContact
	}
	snowflakeID, err := s.gen.Next()
	if err != nil {
		return nil, err
	}
	ticketNo := id.FormatNo("T", snowflakeID)
	t, err := s.repo.Create(ctx, ticketNo, CreateInput{
		UserID: userID, GuestContact: req.GetGuestContact(),
		Type: req.GetType(), OrderID: req.GetOrderId(), ProductID: req.GetProductId(),
		Content: req.GetContent(), Attachments: req.GetAttachmentMediaIds(),
	})
	if err != nil {
		return nil, err
	}
	s.publish(ctx, events.TicketCreated, t, map[string]any{"ticket_no": t.TicketNo})
	return toStorePB(t), nil
}

// ListMyTickets 我的工单。
func (s *StoreTicketService) ListMyTickets(ctx context.Context, req *storefrontv1.ListMyTicketsRequest) (*storefrontv1.ListMyTicketsReply, error) {
	userID := currentUserID(ctx)
	if userID == 0 {
		return &storefrontv1.ListMyTicketsReply{}, nil
	}
	page, size := ticketPage(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListByUser(ctx, userID, page, size)
	if err != nil {
		return nil, err
	}
	reply := &storefrontv1.ListMyTicketsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, t := range rows {
		reply.Tickets = append(reply.Tickets, toStorePB(t))
	}
	return reply, nil
}

// GetTicket 详情（归属校验 + 内部备注过滤）。
func (s *StoreTicketService) GetTicket(ctx context.Context, req *storefrontv1.GetTicketRequest) (*storefrontv1.GetTicketReply, error) {
	t, err := s.repo.GetByNo(ctx, req.GetTicketNo())
	if err != nil {
		return nil, err
	}
	if !s.canAccess(ctx, t) {
		return nil, ErrNotFound // 越权视同不存在（防枚举）
	}
	msgs, err := s.repo.MessagesUserVisible(ctx, t.ID) // 内部备注过滤
	if err != nil {
		return nil, err
	}
	reply := &storefrontv1.GetTicketReply{Ticket: toStorePB(t)}
	if currentUserID(ctx) != 0 && t.UserID == currentUserID(ctx) && (t.Status == ticket.StatusOpen || t.Status == ticket.StatusProcessing) && t.Priority != ticket.PriorityUrgentPaid {
		fee, err := s.urgentFee(ctx)
		if err != nil {
			reply.UrgentError = "暂时无法读取加急费用，请稍后重试"
		} else {
			reply.UrgentAvailable = true
			reply.UrgentFeeCents = fee
		}
	}
	for _, m := range msgs {
		reply.Messages = append(reply.Messages, toMsgPB(m))
	}
	return reply, nil
}

// ReplyTicket 用户回复。
func (s *StoreTicketService) ReplyTicket(ctx context.Context, req *storefrontv1.ReplyTicketRequest) (*emptypb.Empty, error) {
	t, err := s.assertAccess(ctx, req.GetTicketNo())
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.CreateMessage(ctx, t.ID, "user", currentUserID(ctx), req.GetContent(), req.GetAttachmentMediaIds(), false); err != nil {
		return nil, err
	}
	// resolved 追加回复 → processing 重开
	if string(t.Status) == "resolved" {
		if err := s.repo.Transition(ctx, t.ID, "processing"); err != nil {
			return nil, err
		}
	}
	s.publish(ctx, events.TicketReplied, t, map[string]any{"ticket_no": t.TicketNo, "by": "user"})
	return &emptypb.Empty{}, nil
}

// RateTicket 评价（resolved 后 1-5）。
func (s *StoreTicketService) RateTicket(ctx context.Context, req *storefrontv1.RateTicketRequest) (*emptypb.Empty, error) {
	t, err := s.assertAccess(ctx, req.GetTicketNo())
	if err != nil {
		return nil, err
	}
	if req.GetRating() < 1 || req.GetRating() > 5 {
		return nil, errors.New("ticket.RATING_INVALID")
	}
	if err := s.repo.SetSatisfaction(ctx, t.ID, int8(req.GetRating())); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// PayUrgent confirms the quoted fee and commits wallet debit and priority together.
func (s *StoreTicketService) PayUrgent(ctx context.Context, req *storefrontv1.PayUrgentRequest) (*storefrontv1.PayUrgentReply, error) {
	reply := &storefrontv1.PayUrgentReply{}
	var upgraded *ent.Ticket
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		t, err := s.assertAccess(ctx, req.GetTicketNo())
		if err != nil {
			return err
		}
		userID := currentUserID(ctx)
		if userID == 0 || t.UserID != userID {
			return errors.New("ticket.UNAUTHORIZED: 请使用工单所属账户登录")
		}
		client := data.Client(ctx, s.repo.data)
		reference := "ticket_urgent:" + t.TicketNo
		if t.Priority == ticket.PriorityUrgentPaid {
			entry, err := client.WalletTransaction.Query().Where(wallettransaction.Reference(reference)).Only(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return err
			}
			if entry != nil {
				reply.FeeCents = int64(entry.Amount)
			}
			reply.Paid, reply.AlreadyUrgent = true, true
			return nil
		}
		if t.Status != ticket.StatusOpen && t.Status != ticket.StatusProcessing {
			return errors.New("ticket.NOT_ACTIVE: 已解决或关闭的工单不能加急")
		}
		fee, err := s.urgentFee(ctx)
		if err != nil {
			return fmt.Errorf("ticket.FEE_UNAVAILABLE: 无法读取加急费用: %w", err)
		}
		if req.ExpectedFeeCents == nil || req.GetExpectedFeeCents() != fee {
			return errors.New("ticket.FEE_CHANGED: 请刷新页面后重新确认加急费用")
		}
		n, err := client.Ticket.Update().Where(ticket.ID(t.ID), ticket.UserID(userID), ticket.PriorityNEQ(ticket.PriorityUrgentPaid), ticket.StatusIn(ticket.StatusOpen, ticket.StatusProcessing)).
			SetPriority(ticket.PriorityUrgentPaid).SetSLADueAt(time.Now().UTC().Add(urgentSLAHours * time.Hour)).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("ticket.CONCURRENT_UPDATE: 工单状态已变化，请刷新重试")
		}
		if fee > 0 {
			if s.wallet == nil {
				return errors.New("ticket.WALLET_UNAVAILABLE")
			}
			if err := s.wallet.DebitInTx(ctx, walletport.Entry{UserID: userID, Direction: "out", Type: "ticket_urgent", Amount: money.Cents(fee), Reference: reference}); err != nil {
				return err
			}
		}
		reply.Paid, reply.FeeCents = true, fee
		upgraded = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	if upgraded != nil {
		s.publish(ctx, events.TicketReplied, upgraded, map[string]any{"ticket_no": upgraded.TicketNo, "urgent": true})
	}
	return reply, nil
}

// Read the scalar settings value strictly, accepting the legacy object shape.
// Missing configuration defaults to free; a read/parse error must never become free.
func (s *StoreTicketService) urgentFee(ctx context.Context) (int64, error) {
	if s.settings == nil {
		return 0, errors.New("settings unavailable")
	}
	raw, err := s.settings.GetDefault(ctx, urgentFeeGroup, urgentFeeKey, json.RawMessage("0"))
	if err != nil {
		return 0, err
	}
	var fee *int64
	if err = json.Unmarshal(raw, &fee); err != nil {
		var legacy struct {
			Fee *int64 `json:"urgent_fee"`
		}
		if err = json.Unmarshal(raw, &legacy); err != nil {
			return 0, err
		}
		fee = legacy.Fee
	}
	if fee == nil || *fee < 0 {
		return 0, errors.New("invalid urgent fee")
	}
	return *fee, nil
}

// canAccess 归属判定（本人或游客无主单按号访问——游客凭单号+联系方式创建后即持有）。
func (s *StoreTicketService) canAccess(ctx context.Context, t *ent.Ticket) bool {
	userID := currentUserID(ctx)
	if t.UserID != 0 {
		return userID == t.UserID
	}
	return true // 游客单：单号即凭据（雪花不可枚举）
}

func (s *StoreTicketService) assertAccess(ctx context.Context, ticketNo string) (*ent.Ticket, error) {
	t, err := s.repo.GetByNo(ctx, ticketNo)
	if err != nil {
		return nil, err
	}
	if !s.canAccess(ctx, t) {
		return nil, ErrNotFound
	}
	return t, nil
}

// publish 工单事件（notify 消费：用户通知/客服告警）。
func (s *StoreTicketService) publish(ctx context.Context, typ string, t *ent.Ticket, extra map[string]any) {
	if s.outbox == nil {
		return
	}
	payload := map[string]any{"ticket_no": t.TicketNo, "ticket_id": t.ID, "type": string(t.Type)}
	for k, v := range extra {
		payload[k] = v
	}
	raw, err := jsonMarshalTicket(payload)
	if err != nil {
		return
	}
	agg := "ticket:" + t.TicketNo
	_ = s.outbox.Write(ctx, "ticket", typ, agg, agg+":"+typ, raw)
}

func currentUserID(ctx context.Context) uint64 {
	if claims := identity.ClaimsFromContext(ctx); claims != nil {
		return claims.Subject
	}
	return 0
}

func toStorePB(t *ent.Ticket) *storefrontv1.Ticket {
	p := &storefrontv1.Ticket{
		Id: t.ID, TicketNo: t.TicketNo, Type: string(t.Type),
		Priority: string(t.Priority), Status: string(t.Status),
		OrderId: t.OrderID, ProductId: t.ProductID,
		Satisfaction: int32(t.Satisfaction), CreatedAt: t.CreatedAt.Unix(),
	}
	if !t.FirstReplyAt.IsZero() {
		p.FirstReplyAt = t.FirstReplyAt.Unix()
	}
	if !t.SLADueAt.IsZero() {
		p.SlaDueAt = t.SLADueAt.Unix()
	}
	return p
}

func toMsgPB(m *ent.TicketMessage) *storefrontv1.TicketMessage {
	return &storefrontv1.TicketMessage{
		Id: m.ID, SenderType: string(m.SenderType), Content: m.Content,
		Attachments: m.Attachments, CreatedAt: m.CreatedAt.Unix(),
	}
}

func ticketPage(page, pageSize int32) (int, int) {
	p := int(page)
	if p < 1 {
		p = 1
	}
	ps := int(pageSize)
	if ps < 1 {
		ps = 20
	}
	if ps > 100 {
		ps = 100
	}
	return p, ps
}

func jsonUnmarshalTicket(raw []byte, v any) error { return json.Unmarshal(raw, v) }
func jsonMarshalTicket(v any) ([]byte, error)     { return json.Marshal(v) }
