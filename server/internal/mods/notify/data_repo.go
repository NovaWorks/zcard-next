package notify

// 仓储（）：站内信、模板、发送日志。

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notification"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notifybroadcast"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notifytemplate"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
)

// 哨兵错误。
var (
	ErrNotFound = errors.New("notify: 记录不存在")
)

// NotifyRepo 通知仓储。
type NotifyRepo struct {
	data *data.Data
}

// NewNotifyRepo 构造。
func NewNotifyRepo(d *data.Data) *NotifyRepo { return &NotifyRepo{data: d} }

// ── 站内信 ────────────────────────────────────────────────

// CreateInbox 写站内信。
func (r *NotifyRepo) CreateInbox(ctx context.Context, userID uint64, title, content, sourceType string, sourceID uint64) error {
	create := data.Client(ctx, r.data).Notification.Create().
		SetUserID(userID).
		SetTitle(title).
		SetContent(content)
	if sourceType != "" {
		create.SetSourceType(sourceType)
	}
	if sourceID > 0 {
		create.SetSourceID(sourceID)
	}
	_, err := create.Save(ctx)
	return err
}

// ListInbox 用户站内信分页。
func (r *NotifyRepo) ListInbox(ctx context.Context, userID uint64, unreadOnly bool, page, size int) ([]*ent.Notification, int, error) {
	q := data.Client(ctx, r.data).Notification.Query().
		Where(notification.UserID(userID)).
		Order(ent.Desc(notification.FieldID))
	if unreadOnly {
		q = q.Where(notification.ReadAtIsNil())
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// UnreadCount 未读数（铃铛）。
func (r *NotifyRepo) UnreadCount(ctx context.Context, userID uint64) (int, error) {
	return data.Client(ctx, r.data).Notification.Query().
		Where(notification.UserID(userID), notification.ReadAtIsNil()).
		Count(ctx)
}

// MarkRead 标记已读（单条/全部：id=0 全部）。
func (r *NotifyRepo) MarkRead(ctx context.Context, userID, id uint64) error {
	q := data.Client(ctx, r.data).Notification.Update().
		Where(notification.UserID(userID), notification.ReadAtIsNil())
	if id > 0 {
		q = q.Where(notification.ID(id))
	}
	_, err := q.SetReadAt(time.Now().UTC()).Save(ctx)
	return err
}

// ── 模板 ──────────────────────────────────────────────────

// UpsertTemplate 创建/更新模板（UNIQUE(event_type, channel, locale) 语义）。
func (r *NotifyRepo) UpsertTemplate(ctx context.Context, eventType, channel, locale, subjectTpl, bodyTpl string, enabled bool) (*ent.NotifyTemplate, error) {
	existing, err := data.Client(ctx, r.data).NotifyTemplate.Query().
		Where(
			notifytemplate.EventType(eventType),
			notifytemplate.ChannelEQ(notifytemplate.Channel(channel)),
			notifytemplate.Locale(locale),
		).
		Only(ctx)
	if ent.IsNotFound(err) {
		return data.Client(ctx, r.data).NotifyTemplate.Create().
			SetEventType(eventType).
			SetChannel(notifytemplate.Channel(channel)).
			SetLocale(locale).
			SetSubjectTpl(subjectTpl).
			SetBodyTpl(bodyTpl).
			SetEnabled(enabled).
			Save(ctx)
	}
	if err != nil {
		return nil, err
	}
	return data.Client(ctx, r.data).NotifyTemplate.UpdateOneID(existing.ID).
		SetSubjectTpl(subjectTpl).
		SetBodyTpl(bodyTpl).
		SetEnabled(enabled).
		Save(ctx)
}

// Template 查模板（事件 × 通道 × 语言；locale 回落 zh_CN）。
func (r *NotifyRepo) Template(ctx context.Context, eventType, channel, locale string) (*ent.NotifyTemplate, error) {
	t, err := data.Client(ctx, r.data).NotifyTemplate.Query().
		Where(
			notifytemplate.EventType(eventType),
			notifytemplate.ChannelEQ(notifytemplate.Channel(channel)),
			notifytemplate.Locale(locale),
			notifytemplate.Enabled(true),
		).
		Only(ctx)
	if ent.IsNotFound(err) && locale != "zh_CN" {
		return r.Template(ctx, eventType, channel, "zh_CN")
	}
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

// ListTemplates 模板列表。
func (r *NotifyRepo) ListTemplates(ctx context.Context) ([]*ent.NotifyTemplate, error) {
	return data.Client(ctx, r.data).NotifyTemplate.Query().
		Order(ent.Asc(notifytemplate.FieldEventType), ent.Asc(notifytemplate.FieldChannel)).
		All(ctx)
}

// ── 发送日志 ──────────────────────────────────────────────

// LogInput 日志写入参数。
type LogInput struct {
	EventType    string
	BizType      string
	BizID        uint64
	Channel      string
	Recipient    string
	Locale       string
	Subject      string
	Body         string
	Status       string // pending | sent | failed | skipped
	ErrorMessage string
	Variables    map[string]string
}

// WriteLog 落发送日志（每发送一条一行）。
func (r *NotifyRepo) WriteLog(ctx context.Context, in LogInput) error {
	create := data.Client(ctx, r.data).NotificationLog.Create().
		SetEventType(in.EventType).
		SetChannel(notificationlog.Channel(in.Channel)).
		SetRecipient(in.Recipient).
		SetLocale(in.Locale).
		SetSubject(in.Subject).
		SetBody(in.Body).
		SetStatus(notificationlog.Status(in.Status))
	if in.BizType != "" {
		create.SetBizType(in.BizType)
	}
	if in.BizID > 0 {
		create.SetBizID(in.BizID)
	}
	if in.ErrorMessage != "" {
		create.SetErrorMessage(in.ErrorMessage)
	}
	if in.Variables != nil {
		vars := make(map[string]any, len(in.Variables))
		for k, v := range in.Variables {
			vars[k] = v
		}
		create.SetVariables(vars)
	}
	_, err := create.Save(ctx)
	return err
}

// ListLogs 日志查询（按状态/事件筛选）。
func (r *NotifyRepo) ListLogs(ctx context.Context, status, eventType string, page, size int) ([]*ent.NotificationLog, int, error) {
	q := data.Client(ctx, r.data).NotificationLog.Query().
		Order(ent.Desc(notificationlog.FieldID))
	if status != "" {
		q = q.Where(notificationlog.StatusEQ(notificationlog.Status(status)))
	}
	if eventType != "" {
		q = q.Where(notificationlog.EventType(eventType))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// jsonUnmarshal 防御性解析（channel.go 使用）。
func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }

// ── 群发任务（）─────────────────────────────────────────

// CreateBroadcast 创建群发任务（pending；audience 预估回填）。
func (r *NotifyRepo) CreateBroadcast(ctx context.Context, in BroadcastInput, audience int64) (*ent.NotifyBroadcast, error) {
	channels := in.Channels
	if channels == nil {
		channels = []string{}
	}
	targets := in.TargetIDs
	if targets == nil {
		targets = []uint64{}
	}
	create := data.Client(ctx, r.data).NotifyBroadcast.Create().
		SetTitle(in.Title).
		SetContent(in.Content).
		SetChannels(channels).
		SetTargetType(notifybroadcast.TargetType(in.TargetType)).
		SetTargetIds(targets).
		SetStatus(notifybroadcast.StatusPending).
		SetCreatedBy(in.CreatedBy).
		SetAudience(audience)
	if !in.ScheduledAt.IsZero() {
		create.SetScheduledAt(in.ScheduledAt)
	}
	return create.Save(ctx)
}

// GetBroadcast 群发详情。
func (r *NotifyRepo) GetBroadcast(ctx context.Context, id uint64) (*ent.NotifyBroadcast, error) {
	b, err := data.Client(ctx, r.data).NotifyBroadcast.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrBroadcastNotFound
		}
		return nil, err
	}
	return b, nil
}

// ListBroadcasts 群发列表。
func (r *NotifyRepo) ListBroadcasts(ctx context.Context, page, size int) ([]*ent.NotifyBroadcast, int, error) {
	q := data.Client(ctx, r.data).NotifyBroadcast.Query().Order(ent.Desc(notifybroadcast.FieldID))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// SetBroadcastStatus 状态迁移（startedAt 非空时回填）。
func (r *NotifyRepo) SetBroadcastStatus(ctx context.Context, id uint64, status string, startedAt time.Time) (*ent.NotifyBroadcast, error) {
	upd := data.Client(ctx, r.data).NotifyBroadcast.UpdateOneID(id).
		SetStatus(notifybroadcast.Status(status))
	if !startedAt.IsZero() {
		upd.SetStartedAt(startedAt)
	}
	return upd.Save(ctx)
}

// UpdateBroadcastProgress 进度回填（发送中可观测）。
func (r *NotifyRepo) UpdateBroadcastProgress(ctx context.Context, id uint64, sent, failed int64) error {
	_, err := data.Client(ctx, r.data).NotifyBroadcast.UpdateOneID(id).
		SetSentCount(sent).
		SetFailedCount(failed).
		Save(ctx)
	return err
}

// FinishBroadcast 终态（done + finished_at）。
func (r *NotifyRepo) FinishBroadcast(ctx context.Context, id uint64, sent, failed int64) (*ent.NotifyBroadcast, error) {
	return data.Client(ctx, r.data).NotifyBroadcast.UpdateOneID(id).
		SetStatus(notifybroadcast.StatusDone).
		SetSentCount(sent).
		SetFailedCount(failed).
		SetFinishedAt(time.Now().UTC()).
		Save(ctx)
}

// CountBroadcastTargets 目标计数（all=全部用户；active=正常状态；specified=ID 集合）。
func (r *NotifyRepo) CountBroadcastTargets(ctx context.Context, targetType string, targetIDs []uint64) (int64, error) {
	q := data.Client(ctx, r.data).User.Query()
	switch targetType {
	case "active":
		q = q.Where(user.StatusEQ(user.StatusActive))
	case "specified":
		if len(targetIDs) == 0 {
			return 0, nil
		}
		q = q.Where(user.IDIn(targetIDs...))
	}
	n, err := q.Count(ctx)
	return int64(n), err
}

// BroadcastTargets 目标分页（afterID 游标分批）。
func (r *NotifyRepo) BroadcastTargets(ctx context.Context, targetType string, targetIDs []uint64, afterID uint64, limit int) ([]*ent.User, error) {
	q := data.Client(ctx, r.data).User.Query().
		Where(user.IDGT(afterID)).
		Order(ent.Asc(user.FieldID)).
		Limit(limit)
	switch targetType {
	case "active":
		q = q.Where(user.StatusEQ(user.StatusActive))
	case "specified":
		if len(targetIDs) == 0 {
			return nil, nil
		}
		q = q.Where(user.IDIn(targetIDs...))
	}
	return q.All(ctx)
}

// ListDueBroadcasts 到期待发（定时群发 cron 扫描；scheduled_at 为空视为立即）。
func (r *NotifyRepo) ListDueBroadcasts(ctx context.Context, now time.Time) ([]uint64, error) {
	rows, err := data.Client(ctx, r.data).NotifyBroadcast.Query().
		Where(
			notifybroadcast.StatusEQ(notifybroadcast.StatusPending),
			notifybroadcast.Or(
				notifybroadcast.ScheduledAtIsNil(),
				notifybroadcast.ScheduledAtLTE(now),
			),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(rows))
	for _, b := range rows {
		ids = append(ids, b.ID)
	}
	return ids, nil
}
