package notify

// 管理面 + 前台铃铛 API（）：模板 CRUD/预览、日志查询/重发、站内信。

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/go-kratos/kratos/v3/errors"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminNotifyService 管理面通知服务。
type AdminNotifyService struct {
	adminv1.UnimplementedAdminNotifyServiceServer
	repo      *NotifyRepo
	disp      *Dispatcher
	broadcast *BroadcastService
}

// NewAdminNotifyService 构造。
func NewAdminNotifyService(repo *NotifyRepo, disp *Dispatcher, broadcast *BroadcastService) *AdminNotifyService {
	return &AdminNotifyService{repo: repo, disp: disp, broadcast: broadcast}
}

// UpsertTemplate 创建/更新模板。
func (s *AdminNotifyService) UpsertTemplate(ctx context.Context, req *adminv1.UpsertNotifyTemplateRequest) (*adminv1.NotifyTemplate, error) {
	locale := req.GetLocale()
	if locale == "" {
		locale = "zh_CN"
	}
	t, err := s.repo.UpsertTemplate(ctx, req.GetEventType(), req.GetChannel(), locale, req.GetSubjectTpl(), req.GetBodyTpl(), req.GetEnabled())
	if err != nil {
		return nil, err
	}
	return toTemplatePB(t), nil
}

// ListTemplates 模板列表。
func (s *AdminNotifyService) ListTemplates(ctx context.Context, _ *emptypb.Empty) (*adminv1.ListNotifyTemplatesReply, error) {
	rows, err := s.repo.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListNotifyTemplatesReply{}
	for _, t := range rows {
		reply.Templates = append(reply.Templates, toTemplatePB(t))
	}
	return reply, nil
}

// PreviewTemplate 渲染预览（样例变量；不发送）。
func (s *AdminNotifyService) PreviewTemplate(ctx context.Context, req *adminv1.PreviewTemplateRequest) (*adminv1.PreviewTemplateReply, error) {
	locale := req.GetLocale()
	if locale == "" {
		locale = "zh_CN"
	}
	tpl, err := s.repo.Template(ctx, req.GetEventType(), req.GetChannel(), locale)
	if err != nil {
		return &adminv1.PreviewTemplateReply{Error: "模板不存在"}, nil
	}
	sample := SampleVars(req.GetEventType())
	return &adminv1.PreviewTemplateReply{
		Subject: RenderTemplate(tpl.SubjectTpl, sample),
		Body:    RenderTemplate(tpl.BodyTpl, sample),
	}, nil
}

// ListLogs 日志查询。
func (s *AdminNotifyService) ListLogs(ctx context.Context, req *adminv1.ListNotifyLogsRequest) (*adminv1.ListNotifyLogsReply, error) {
	page, size := notifyPageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListLogs(ctx, req.GetStatus(), req.GetEventType(), page, size, req.GetChannel())
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListNotifyLogsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, l := range rows {
		target, _ := telegramLogTarget(l)
		reply.Logs = append(reply.Logs, &adminv1.NotifyLog{
			Id: l.ID, TopicId: target.TopicID, EventType: l.EventType, BizType: l.BizType, BizId: l.BizID,
			Channel: string(l.Channel), Recipient: l.Recipient, Locale: l.Locale,
			Subject: l.Subject, Status: string(l.Status), ErrorMessage: l.ErrorMessage,
			CreatedAt: l.CreatedAt.Unix(), Attempts: int32(l.Attempts), NextAttemptAt: timeOrZero(l.NextAttemptAt), MessageId: l.MessageID, Retryable: l.DeliveryKey != nil && l.Channel == notificationlog.ChannelTelegram && l.Status == notificationlog.StatusFailed,
		})
	}
	return reply, nil
}

// ResendLog 重发（原变量重投）。
func (s *AdminNotifyService) ResendLog(ctx context.Context, req *adminv1.ResendNotifyLogRequest) (*emptypb.Empty, error) {
	row, err := data.Client(ctx, s.repo.data).NotificationLog.Get(ctx, req.GetId())
	if err != nil {
		return nil, errors.NotFound("notify.NOT_FOUND", "发送记录不存在")
	}
	c := s.disp.telegram()
	if c == nil {
		return nil, errors.BadRequest("notify.NOT_CONFIGURED", "Telegram 通道未配置")
	}
	cfg, err := c.tgConfig(ctx)
	if err != nil {
		return nil, errors.BadRequest("notify.NOT_CONFIGURED", "Telegram 配置读取失败")
	}
	target, err := telegramLogTarget(row)
	if err != nil || !telegramTargetEnabled(cfg, row.EventType, target) {
		return nil, errors.BadRequest("notify.TARGET_REMOVED", "通知已关闭或原接收位置已移除，请核对原 Chat ID 和 Topic ID")
	}
	n, err := data.Client(ctx, s.repo.data).NotificationLog.Update().Where(notificationlog.ID(req.GetId()), notificationlog.ChannelEQ(notificationlog.ChannelTelegram), notificationlog.DeliveryKeyNotNil(), notificationlog.StatusEQ(notificationlog.StatusFailed)).
		SetStatus(notificationlog.StatusPending).SetAttempts(0).ClearLeaseUntil().SetNextAttemptAt(time.Now().UTC()).Save(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errors.BadRequest("notify.RETRY_UNAVAILABLE", "仅可重试失败的 Telegram 投递任务")
	}
	return &emptypb.Empty{}, nil
}

// StoreNotificationService 前台铃铛。
type StoreNotificationService struct {
	storefrontv1.UnimplementedStoreNotificationServiceServer
	repo *NotifyRepo
}

// NewStoreNotificationService 构造。
func NewStoreNotificationService(repo *NotifyRepo) *StoreNotificationService {
	return &StoreNotificationService{repo: repo}
}

// UnreadCount 未读数。
func (s *StoreNotificationService) UnreadCount(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.UnreadCountReply, error) {
	userID := identityClaimsUserID(ctx)
	if userID == 0 {
		return &storefrontv1.UnreadCountReply{Count: 0}, nil // 游客无铃铛
	}
	n, err := s.repo.UnreadCount(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &storefrontv1.UnreadCountReply{Count: int64(n)}, nil
}

// ListNotifications 列表。
func (s *StoreNotificationService) ListNotifications(ctx context.Context, req *storefrontv1.ListNotificationsRequest) (*storefrontv1.ListNotificationsReply, error) {
	userID := identityClaimsUserID(ctx)
	if userID == 0 {
		return &storefrontv1.ListNotificationsReply{}, nil
	}
	page, size := notifyPageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListInbox(ctx, userID, req.GetUnreadOnly(), page, size)
	if err != nil {
		return nil, err
	}
	reply := &storefrontv1.ListNotificationsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, n := range rows {
		item := &storefrontv1.NotificationItem{
			Id: n.ID, Title: n.Title, Content: n.Content,
			SourceType: n.SourceType, SourceId: n.SourceID,
			CreatedAt: n.CreatedAt.Unix(),
		}
		if !n.ReadAt.IsZero() {
			item.IsRead = true
			item.ReadAt = n.ReadAt.Unix()
		}
		reply.Notifications = append(reply.Notifications, item)
	}
	return reply, nil
}

// MarkRead 已读（id=0 全部）。
func (s *StoreNotificationService) MarkRead(ctx context.Context, req *storefrontv1.MarkReadRequest) (*emptypb.Empty, error) {
	userID := identityClaimsUserID(ctx)
	if userID == 0 {
		return &emptypb.Empty{}, nil
	}
	if err := s.repo.MarkRead(ctx, userID, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// SampleVars 预览样例变量（事件 → 白名单样例值）。
func SampleVars(eventType string) map[string]string {
	base := map[string]string{
		"order_no": "T202608161234567890", "email": "buyer@example.com",
		"user_id": "42", "amount": "1000", "site_name": "ZCard 商店",
	}
	switch eventType {
	case "order.delivered":
		base["cards_count"] = "2"
		base["fetch_url"] = "https://example.com/fetch"
	case "user.registered":
		base["username"] = "newuser"
	}
	return base
}

func toTemplatePB(t *ent.NotifyTemplate) *adminv1.NotifyTemplate {
	return &adminv1.NotifyTemplate{
		Id: t.ID, EventType: t.EventType, Channel: string(t.Channel),
		Locale: t.Locale, SubjectTpl: t.SubjectTpl, BodyTpl: t.BodyTpl,
		Enabled: t.Enabled, UpdatedAt: t.UpdatedAt.Unix(),
	}
}

func notifyPageParams(page, pageSize int32) (int, int) {
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

// identityClaimsUserID 从上下文取用户 ID（storefront user realm JWT；未登录 0）。
func identityClaimsUserID(ctx context.Context) uint64 {
	if claims := identity.ClaimsFromContext(ctx); claims != nil {
		return claims.Subject
	}
	return 0
}

// ── 群发任务（）─────────────────────────────────────────

// EstimateBroadcast 覆盖人数预估。
func (s *AdminNotifyService) EstimateBroadcast(ctx context.Context, req *adminv1.EstimateBroadcastRequest) (*adminv1.EstimateBroadcastReply, error) {
	n, err := s.broadcast.EstimateAudience(ctx, req.GetTargetType(), req.GetTargetIds())
	if err != nil {
		return nil, err
	}
	return &adminv1.EstimateBroadcastReply{Audience: n}, nil
}

// CreateBroadcast 创建群发。
func (s *AdminNotifyService) CreateBroadcast(ctx context.Context, req *adminv1.CreateBroadcastRequest) (*adminv1.Broadcast, error) {
	b, err := s.broadcast.Create(ctx, BroadcastInput{
		Title: req.GetTitle(), Content: req.GetContent(),
		Channels: req.GetChannels(), TargetType: req.GetTargetType(),
		TargetIDs: req.GetTargetIds(),
		ScheduledAt: func() time.Time {
			if req.GetScheduledAt() <= 0 {
				return time.Time{}
			}
			return time.Unix(req.GetScheduledAt(), 0).UTC()
		}(),
	})
	if err != nil {
		return nil, err
	}
	return toBroadcastPB(b), nil
}

// ListBroadcasts 群发列表。
func (s *AdminNotifyService) ListBroadcasts(ctx context.Context, req *adminv1.ListBroadcastsRequest) (*adminv1.ListBroadcastsReply, error) {
	page, size := notifyPageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.broadcast.repo.ListBroadcasts(ctx, page, size)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListBroadcastsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, b := range rows {
		reply.Broadcasts = append(reply.Broadcasts, toBroadcastPB(b))
	}
	return reply, nil
}

// CancelBroadcast 取消群发。
func (s *AdminNotifyService) CancelBroadcast(ctx context.Context, req *adminv1.CancelBroadcastRequest) (*adminv1.Broadcast, error) {
	b, err := s.broadcast.Cancel(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return toBroadcastPB(b), nil
}

func toBroadcastPB(b *ent.NotifyBroadcast) *adminv1.Broadcast {
	p := &adminv1.Broadcast{
		Id: b.ID, Title: b.Title, Content: b.Content, Channels: b.Channels,
		TargetType: string(b.TargetType), TargetIds: b.TargetIds,
		Status: string(b.Status), CreatedBy: b.CreatedBy,
		Audience: b.Audience, SentCount: b.SentCount, FailedCount: b.FailedCount,
		CreatedAt: b.CreatedAt.Unix(),
	}
	if !b.ScheduledAt.IsZero() {
		p.ScheduledAt = b.ScheduledAt.Unix()
	}
	if !b.StartedAt.IsZero() {
		p.StartedAt = b.StartedAt.Unix()
	}
	if !b.FinishedAt.IsZero() {
		p.FinishedAt = b.FinishedAt.Unix()
	}
	return p
}

func timeOrZero(v *time.Time) int64 {
	if v == nil {
		return 0
	}
	return v.Unix()
}
func (s *AdminNotifyService) TestTelegram(ctx context.Context, req *adminv1.TestTelegramRequest) (*adminv1.TestTelegramReply, error) {
	c := s.disp.telegram()
	if c == nil {
		return nil, errors.BadRequest("notify.NOT_CONFIGURED", "Telegram 通道未配置")
	}
	cfg, err := c.tgConfig(ctx)
	if err != nil {
		return nil, errors.BadRequest("notify.NOT_CONFIGURED", "Telegram 配置读取失败")
	}
	if !telegramEnabled(cfg, "telegram.test") || len(telegramTargets(cfg)) == 0 {
		return nil, errors.BadRequest("notify.NOT_CONFIGURED", "请先保存并启用 Telegram 通道、订单通知、Token 和接收 Chat ID")
	}
	targets := telegramTargets(cfg)
	if req.GetChatId() != "" || req.GetTopicId() != 0 {
		target, err := notifyport.NormalizeTelegramTarget(notifyport.TelegramTarget{ChatID: req.GetChatId(), TopicID: req.GetTopicId()})
		if err != nil || !telegramTargetEnabled(cfg, "telegram.test", target) {
			return nil, errors.BadRequest("notify.INVALID_TARGET", "请先保存该接收位置，再发送测试消息")
		}
		targets = []notifyport.TelegramTarget{target}
	}
	eventID := uint64(time.Now().UnixNano())
	reply := &adminv1.TestTelegramReply{}
	err = data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		if err := s.repo.enqueueTelegram(ctx, eventID, "telegram.test", 0, "Telegram 订单通知测试", "这是一条管理员主动发送的测试消息。正式通知包含订单号、商品摘要、金额与订单入口，不包含卡密或查询密码。", targets); err != nil {
			return err
		}
		keys := make([]string, 0, len(targets))
		for _, target := range targets {
			keys = append(keys, telegramDeliveryKey(eventID, target))
		}
		ids, err := data.Client(ctx, s.repo.data).NotificationLog.Query().Where(notificationlog.DeliveryKeyIn(keys...)).IDs(ctx)
		reply.LogIds = ids
		return err
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}
