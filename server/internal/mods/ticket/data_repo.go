package ticket

// 工单仓储（P3-05）：生命周期状态机 + 消息流（内部备注双过滤）+ 自动关闭。

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/ticket"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/ticketmessage"
	mediaport "github.com/NovaWorks/zcard-next/server/internal/mods/media/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/sanitize"
)

// 哨兵错误。
var (
	ErrNotFound     = errors.New("ticket: 工单不存在")
	ErrTransition   = errors.New("ticket: 非法状态迁移")
	ErrGuestContact = errors.New("ticket.GUEST_CONTACT_REQUIRED: 游客工单需留联系方式")
	ErrAlreadyRated = errors.New("ticket: 已评价")
	ErrNotResolved  = errors.New("ticket: 解决后才能评价")
)

// 状态机：open → processing（首次客服回复）→ resolved → closed。
var allowedTicketTransitions = map[string]map[string]bool{
	"open":       {"processing": true, "resolved": true, "closed": true},
	"processing": {"resolved": true, "closed": true},
	"resolved":   {"closed": true, "processing": true}, // processing 回退（追加回复重开）
}

// TicketRepo 工单仓储。
type TicketRepo struct {
	data     *data.Data
	mediaRef mediaport.Referencer // 附件引用计数（nil 跳过）
}

// NewTicketRepo 构造（mediaRef 素材引用计数，P3-06）。
func NewTicketRepo(d *data.Data, mediaRef mediaport.Referencer) *TicketRepo {
	return &TicketRepo{data: d, mediaRef: mediaRef}
}

// CreateInput 创建入参。
type CreateInput struct {
	UserID       uint64 // 0=游客
	GuestContact string
	Type         string // presale | aftersale
	OrderID      uint64
	ProductID    uint64
	Content      string
	Attachments  []uint64
}

// Create 创建工单 + 首条消息（游客联系方式必填；content sanitize）。
func (r *TicketRepo) Create(ctx context.Context, ticketNo string, in CreateInput) (*ent.Ticket, error) {
	if in.UserID == 0 && in.GuestContact == "" {
		return nil, ErrGuestContact
	}
	client := data.Client(ctx, r.data)
	create := client.Ticket.Create().
		SetTicketNo(ticketNo).
		SetType(ticket.Type(in.Type)).
		SetStatus(ticket.StatusOpen).
		SetPriority(ticket.PriorityNormal)
	if in.UserID > 0 {
		create.SetUserID(in.UserID)
	}
	if in.GuestContact != "" {
		create.SetGuestContact(in.GuestContact)
	}
	if in.OrderID > 0 {
		create.SetOrderID(in.OrderID)
	}
	if in.ProductID > 0 {
		create.SetProductID(in.ProductID)
	}
	t, err := create.Save(ctx)
	if err != nil {
		return nil, err
	}
	// 首条消息（user 发起）
	if _, err := r.CreateMessage(ctx, t.ID, "user", in.UserID, in.Content, in.Attachments, false); err != nil {
		return nil, err
	}
	return t, nil
}

// CreateMessage 追加消息（append-only；客服回复触发状态机；content sanitize）。
// senderType: user/admin/system；isInternal 内部备注（用户侧过滤）。
// 返回（工单是否首次客服回复——用于状态迁移，调用方决定）。
func (r *TicketRepo) CreateMessage(ctx context.Context, ticketID uint64, senderType string, senderID uint64, content string, attachments []uint64, isInternal bool) (*ent.TicketMessage, error) {
	if attachments == nil {
		attachments = []uint64{}
	}
	m, err := data.Client(ctx, r.data).TicketMessage.Create().
		SetTicketID(ticketID).
		SetSenderType(ticketmessage.SenderType(senderType)).
		SetNillableSenderID(nilOrZeroU64(senderID)).
		SetContent(sanitize.HTML(content)).
		SetAttachments(attachments).
		SetIsInternal(isInternal).
		Save(ctx)
	if err == nil && r.mediaRef != nil && len(attachments) > 0 {
		_ = r.mediaRef.AddRefs(ctx, attachments) // 附件引用 +1（防误删门禁）
	}
	return m, err
}

// Transition 状态机 CAS（非法迁移拒绝）。
func (r *TicketRepo) Transition(ctx context.Context, id uint64, to string) error {
	client := data.Client(ctx, r.data)
	cur, err := client.Ticket.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return err
	}
	from := string(cur.Status)
	if from == to {
		return nil // 幂等
	}
	if !allowedTicketTransitions[from][to] {
		return fmt.Errorf("%w: %s → %s", ErrTransition, from, to)
	}
	// 首次客服回复回填 first_reply_at + open→processing
	upd := client.Ticket.UpdateOneID(id).SetStatus(ticket.Status(to))
	if to == "processing" && cur.FirstReplyAt.IsZero() {
		upd.SetFirstReplyAt(time.Now().UTC())
	}
	_, err = upd.Save(ctx)
	return err
}

// MarkProcessingOnReply 客服回复后状态推进（open → processing；首答回填）。
func (r *TicketRepo) MarkProcessingOnReply(ctx context.Context, id uint64) error {
	return r.Transition(ctx, id, "processing")
}

// GetByNo 按工单号查。
func (r *TicketRepo) GetByNo(ctx context.Context, ticketNo string) (*ent.Ticket, error) {
	t, err := data.Client(ctx, r.data).Ticket.Query().
		Where(ticket.TicketNo(ticketNo)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

// MessagesUserVisible 用户侧消息流（内部备注过滤——防泄漏双过滤之一）。
func (r *TicketRepo) MessagesUserVisible(ctx context.Context, ticketID uint64) ([]*ent.TicketMessage, error) {
	return data.Client(ctx, r.data).TicketMessage.Query().
		Where(ticketmessage.TicketID(ticketID), ticketmessage.IsInternal(false)).
		Order(ent.Asc(ticketmessage.FieldID)).
		All(ctx)
}

// MessagesAll 客服侧消息流（含内部备注）。
func (r *TicketRepo) MessagesAll(ctx context.Context, ticketID uint64) ([]*ent.TicketMessage, error) {
	return data.Client(ctx, r.data).TicketMessage.Query().
		Where(ticketmessage.TicketID(ticketID)).
		Order(ent.Asc(ticketmessage.FieldID)).
		All(ctx)
}

// ListByUser 用户工单列表。
func (r *TicketRepo) ListByUser(ctx context.Context, userID uint64, page, size int) ([]*ent.Ticket, int, error) {
	q := data.Client(ctx, r.data).Ticket.Query().
		Where(ticket.UserID(userID)).
		Order(ent.Desc(ticket.FieldID))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// ListWorkbench 客服工作台（urgent_paid 置顶 → high → normal → low；状态/类型/订单筛选）。
func (r *TicketRepo) ListWorkbench(ctx context.Context, status, typ string, orderID uint64, page, size int) ([]*ent.Ticket, int, error) {
	q := data.Client(ctx, r.data).Ticket.Query()
	switch status {
	case "":
		q = q.Where(ticket.StatusIn(ticket.StatusOpen, ticket.StatusProcessing)) // 默认未关闭
	case "open", "processing", "resolved", "closed":
		q = q.Where(ticket.StatusEQ(ticket.Status(status)))
	}
	if typ != "" {
		q = q.Where(ticket.TypeEQ(ticket.Type(typ)))
	}
	if orderID > 0 {
		q = q.Where(ticket.OrderID(orderID))
	}
	// 优先级排序（CASE 语义经 FieldOrder + 自定义顺序：ent 无 CASE——查回后排序）
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(ticket.FieldID)).
		Offset((page - 1) * size).Limit(size * 2). // 多取一页余量供排序后裁剪
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	sortByPriority(rows)
	if len(rows) > size {
		rows = rows[:size]
	}
	return rows, total, nil
}

// sortByPriority urgent_paid → high → normal → low 稳定排序。
func sortByPriority(rows []*ent.Ticket) {
	rank := map[string]int{"urgent_paid": 0, "high": 1, "normal": 2, "low": 3}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rank[string(rows[j-1].Priority)] > rank[string(rows[j].Priority)]; j-- {
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
}

// SetPriority 设置优先级（付费加急路径）。
func (r *TicketRepo) SetPriority(ctx context.Context, id uint64, priority string) error {
	_, err := data.Client(ctx, r.data).Ticket.UpdateOneID(id).
		SetPriority(ticket.Priority(priority)).
		Save(ctx)
	return err
}

// SetSatisfaction 评价（resolved 后 1-5；幂等拒绝重复评价）。
func (r *TicketRepo) SetSatisfaction(ctx context.Context, id uint64, rating int8) error {
	t, err := data.Client(ctx, r.data).Ticket.Get(ctx, id)
	if err != nil {
		return ErrNotFound
	}
	if t.Satisfaction != 0 {
		return ErrAlreadyRated
	}
	if string(t.Status) != "resolved" {
		return ErrNotResolved
	}
	_, err = data.Client(ctx, r.data).Ticket.UpdateOneID(id).SetSatisfaction(rating).Save(ctx)
	return err
}

// SetSLA 回填 SLA 时限（付费加急更短；M4 告警消费）。
func (r *TicketRepo) SetSLA(ctx context.Context, id uint64, due time.Time) error {
	_, err := data.Client(ctx, r.data).Ticket.UpdateOneID(id).SetSLADueAt(due).Save(ctx)
	return err
}

// ListAutoCloseable resolved 超 N 天待自动关闭（cron）。
func (r *TicketRepo) ListAutoCloseable(ctx context.Context, olderThan time.Time, limit int) ([]*ent.Ticket, error) {
	return data.Client(ctx, r.data).Ticket.Query().
		Where(ticket.StatusEQ(ticket.StatusResolved), ticket.UpdatedAtLT(olderThan)).
		Limit(limit).
		All(ctx)
}

// Stats 工单统计（响应时长/解决率——dashboard 消费）。
type Stats struct {
	Total     int64
	Resolved  int64
	AvgReplyS time.Duration // 首响平均（有 first_reply_at 的单）
}

// ComputeStats 统计。
func (r *TicketRepo) ComputeStats(ctx context.Context) (*Stats, error) {
	client := data.Client(ctx, r.data)
	total, err := client.Ticket.Query().Count(ctx)
	if err != nil {
		return nil, err
	}
	resolved, err := client.Ticket.Query().
		Where(ticket.StatusIn(ticket.StatusResolved, ticket.StatusClosed)).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	replied, err := client.Ticket.Query().
		Where(ticket.FirstReplyAtNotNil()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	var sum time.Duration
	for _, t := range replied {
		sum += t.FirstReplyAt.Sub(t.CreatedAt)
	}
	avg := time.Duration(0)
	if len(replied) > 0 {
		avg = sum / time.Duration(len(replied))
	}
	return &Stats{Total: int64(total), Resolved: int64(resolved), AvgReplyS: avg}, nil
}

func nilOrZeroU64(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}

// OrderIDByNo 按订单号反查 ID（工作台筛选拼接输入）。
func (r *TicketRepo) OrderIDByNo(ctx context.Context, orderNo string) uint64 {
	o, err := data.Client(ctx, r.data).Order.Query().
		Where(order.OrderNo(orderNo)).Only(ctx)
	if err != nil {
		return 0
	}
	return o.ID
}
