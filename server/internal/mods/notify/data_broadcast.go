package notify

// T4 定向群发（P2-05）：目标筛选（全部/活跃/指定会员）+ 定时发送（cron 扫描）+
// 覆盖人数预估 + 取消（未到定时点）+ 送达统计。
//
// 执行：BroadcastTaskType 任务（default 队列；降级进程内）；分批发送（100/批）
// 每目标每通道一条 notification_logs（biz_type=broadcast）。
// 站内信直写 notifications；email/sms/telegram 经通道投递（失败计数不阻断）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notifybroadcast"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

// BroadcastTaskType 群发任务类型（default 队列）。
const BroadcastTaskType = "notify.broadcast"

// 群发批次大小。
const broadcastBatch = 100

// 哨兵错误。
var (
	ErrBroadcastNotFound = errors.New("notify: 群发任务不存在")
	ErrBroadcastStarted  = errors.New("notify: 群发已开始，不可取消")
)

// BroadcastService 群发服务。
type BroadcastService struct {
	repo *NotifyRepo
	disp *Dispatcher
	enq  queue.Enqueuer
}

// NewBroadcastService 构造。
func NewBroadcastService(repo *NotifyRepo, disp *Dispatcher, enq queue.Enqueuer) *BroadcastService {
	return &BroadcastService{repo: repo, disp: disp, enq: enq}
}

// BroadcastInput 创建入参。
type BroadcastInput struct {
	Title       string
	Content     string
	Channels    []string
	TargetType  string // all | active | specified
	TargetIDs   []uint64
	ScheduledAt time.Time // 零值 = 立即
	CreatedBy   uint64
}

// EstimateAudience 覆盖人数预估（创建前预览）。
func (s *BroadcastService) EstimateAudience(ctx context.Context, targetType string, targetIDs []uint64) (int64, error) {
	return s.repo.CountBroadcastTargets(ctx, targetType, targetIDs)
}

// Create 创建群发任务（定时 → cron 扫描触发；立即 → 直接入队）。
func (s *BroadcastService) Create(ctx context.Context, in BroadcastInput) (*ent.NotifyBroadcast, error) {
	if in.Title == "" || in.Content == "" {
		return nil, errors.New("notify.TITLE_CONTENT_REQUIRED")
	}
	if len(in.Channels) == 0 {
		return nil, errors.New("notify.CHANNELS_REQUIRED")
	}
	if in.TargetType == "specified" && len(in.TargetIDs) == 0 {
		return nil, errors.New("notify.TARGET_IDS_REQUIRED")
	}
	// 预估覆盖人数（创建即回填）
	audience, err := s.EstimateAudience(ctx, in.TargetType, in.TargetIDs)
	if err != nil {
		return nil, err
	}
	b, err := s.repo.CreateBroadcast(ctx, in, audience)
	if err != nil {
		return nil, err
	}
	// 立即发送：入队；定时：cron 扫描（ScanDueBroadcasts）
	if in.ScheduledAt.IsZero() || !in.ScheduledAt.After(time.Now().UTC()) {
		if err := s.enqueueBroadcast(ctx, b.ID); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// Cancel 取消（仅 pending；已开始不可取消）。
func (s *BroadcastService) Cancel(ctx context.Context, id uint64) (*ent.NotifyBroadcast, error) {
	b, err := s.repo.GetBroadcast(ctx, id)
	if err != nil {
		return nil, err
	}
	if string(b.Status) != "pending" {
		return nil, ErrBroadcastStarted
	}
	return s.repo.SetBroadcastStatus(ctx, id, "canceled", time.Time{})
}

// ScanDue 定时群发扫描（cron 每分钟）：到期 pending → 入队。
func (s *BroadcastService) ScanDue(ctx context.Context) {
	ids, err := s.repo.ListDueBroadcasts(ctx, time.Now().UTC())
	if err != nil {
		return
	}
	for _, id := range ids {
		if err := s.enqueueBroadcast(ctx, id); err != nil {
			continue
		}
	}
}

// RunBroadcastTask 队列任务入口（payload {"broadcast_id": N}）。
func (s *BroadcastService) RunBroadcastTask(ctx context.Context, payload []byte) error {
	var req struct {
		BroadcastID uint64 `json:"broadcast_id"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("notify.broadcast: 解析载荷失败: %w", err)
	}
	if req.BroadcastID == 0 {
		return nil
	}
	return s.Execute(ctx, req.BroadcastID)
}

// Execute 执行群发：锁定 sending → 分批投递 → 统计回填 → done。
func (s *BroadcastService) Execute(ctx context.Context, broadcastID uint64) error {
	b, err := s.repo.GetBroadcast(ctx, broadcastID)
	if err != nil {
		return err
	}
	// 幂等：非 pending 直接 ACK（重复投递）
	if string(b.Status) != "pending" {
		return nil
	}
	b, err = s.repo.SetBroadcastStatus(ctx, broadcastID, "sending", time.Now().UTC())
	if err != nil {
		return err
	}

	var sent, failed int64
	var lastID uint64
	for {
		// 取消检查（发送中允许取消：批次间检查，剩余目标不再投递）
		cur, err := s.repo.GetBroadcast(ctx, broadcastID)
		if err == nil && string(cur.Status) == "canceled" {
			break
		}
		targets, err := s.repo.BroadcastTargets(ctx, string(b.TargetType), b.TargetIds, lastID, broadcastBatch)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			break
		}
		for _, u := range targets {
			lastID = u.ID
			sentOne, failOne := s.deliverOne(ctx, b, u)
			sent += sentOne
			failed += failOne
		}
		// 统计增量回填（进度可观测）
		_ = s.repo.UpdateBroadcastProgress(ctx, broadcastID, sent, failed)
		if len(targets) < broadcastBatch {
			break
		}
	}
	_, err = s.repo.FinishBroadcast(ctx, broadcastID, sent, failed)
	return err
}

// deliverOne 单目标投递（多通道逐个；inbox 直写，其余经 Dispatcher）。
func (s *BroadcastService) deliverOne(ctx context.Context, b *ent.NotifyBroadcast, u *ent.User) (sent, failed int64) {
	for _, ch := range b.Channels {
		msg := broadcastMessage(b, ch, u)
		if ch == "inbox" {
			if err := s.repo.CreateInbox(ctx, u.ID, b.Title, plainText(b.Content), "broadcast", b.ID); err != nil {
				failed++
				_ = s.repo.WriteLog(ctx, logOf(msg, "failed", err.Error()))
				continue
			}
			sent++
			_ = s.repo.WriteLog(ctx, logOf(msg, "sent", ""))
			continue
		}
		if err := s.disp.Send(ctx, msg); err != nil {
			failed++
			continue
		}
		sent++
	}
	return sent, failed
}

func broadcastMessage(b *ent.NotifyBroadcast, channel string, u *ent.User) notifyport.Message {
	msg := notifyport.Message{
		EventType: "broadcast", Channel: channel, Locale: "zh_CN",
		Subject: b.Title, Body: b.Content,
		BizType: "broadcast", BizID: b.ID, UserID: u.ID,
		Variables: map[string]string{"user_id": fmt.Sprint(u.ID), "username": u.Username},
	}
	if channel == "email" {
		msg.Recipient = u.Email // 收件人按通道解析
	}
	return msg
}

// enqueueBroadcast 入队（有 Redis 走队列；降级进程内异步）。
func (s *BroadcastService) enqueueBroadcast(ctx context.Context, id uint64) error {
	payload, _ := json.Marshal(map[string]uint64{"broadcast_id": id})
	if s.enq == nil || !s.enq.Enabled() {
		go func() {
			runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
			defer cancel()
			if err := s.Execute(runCtx, id); err != nil {
				_ = err
			}
		}()
		return nil
	}
	return s.enq.Enqueue(ctx, queue.Task{
		Type:      BroadcastTaskType,
		Payload:   payload,
		Queue:     queue.QueueDefault,
		DedupeKey: BroadcastTaskType + ":" + fmt.Sprint(id),
	})
}

var _ = data.Client
var _ = user.FieldID
var _ = notifybroadcast.FieldID
