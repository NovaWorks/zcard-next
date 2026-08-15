package data

// P0-01 验收必测项：事务性 Outbox（回滚不残留/dedupe 幂等）、relay 投递、消费幂等、
// SyncQueue 死信落库。SQLite 内存库跑单元矩阵（MySQL/PG 集成线 M1 随 CI 点亮）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/outboxevent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
	"modernc.org/sqlite"
)

// sqlite3Alias modernc 驱动注册名别名（enttest/atlas 生态按 "sqlite3" 查找）。
func init() {
	if !slices.Contains(sql.Drivers(), "sqlite3") {
		sql.Register("sqlite3", &sqlite.Driver{})
	}
}

// newTestData 内存 SQLite 构造 Data（含 schema 建表——测试不走版本化迁移线）。
func newTestData(t *testing.T) *Data {
	t.Helper()
	handle, err := db.SQLite.Open("file:test?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	drv := entsql.OpenDB(dialect.SQLite, handle)
	client := ent.NewClient(ent.Driver(drv))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Data{Client: client, DB: handle, Dialect: db.SQLite}
}

func TestOutboxWriteRollbackKeepsNothing(t *testing.T) {
	d := newTestData(t)
	w := NewOutboxWriter(d)
	ctx := context.Background()

	// 事务回滚 → 事件不残留
	err := Tx(ctx, d, func(ctx context.Context) error {
		if err := w.Write(ctx, "order", events.OrderCreated, "S1", "order:1:created", json.RawMessage(`{}`)); err != nil {
			return err
		}
		return errors.New("业务失败模拟回滚")
	})
	if err == nil {
		t.Fatal("应返回业务错误")
	}
	n, _ := d.Client.OutboxEvent.Query().Count(ctx)
	if n != 0 {
		t.Fatalf("事务回滚后事件残留：%d", n)
	}

	// 事务提交 → 事件恰好一条
	if err := Tx(ctx, d, func(ctx context.Context) error {
		return w.Write(ctx, "order", events.OrderCreated, "S1", "order:1:created", json.RawMessage(`{}`))
	}); err != nil {
		t.Fatal(err)
	}
	// 同 dedupe_key 重复发布 → 幂等返回 nil 且不新增
	if err := Tx(ctx, d, func(ctx context.Context) error {
		return w.Write(ctx, "order", events.OrderCreated, "S1", "order:1:created", json.RawMessage(`{}`))
	}); err != nil {
		t.Fatal(err)
	}
	n, _ = d.Client.OutboxEvent.Query().Count(ctx)
	if n != 1 {
		t.Fatalf("dedupe 幂等失败：期望 1 条，实际 %d", n)
	}
}

// captureQueue 捕获型 Enqueuer（测试 relay 投递与失败重试）。
type captureQueue struct {
	mu    sync.Mutex
	tasks []queue.Task
	fail  atomic.Int32 // >0 时入队失败（模拟队列不可用）
}

func (c *captureQueue) Enqueue(_ context.Context, task queue.Task) error {
	if c.fail.Load() > 0 {
		return fmt.Errorf("队列不可用（模拟）")
	}
	c.mu.Lock()
	c.tasks = append(c.tasks, task)
	c.mu.Unlock()
	return nil
}
func (*captureQueue) Enabled() bool { return false }

func TestRelayDeliversOnce(t *testing.T) {
	d := newTestData(t)
	w := NewOutboxWriter(d)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := Tx(ctx, d, func(ctx context.Context) error {
			return w.Write(ctx, "order", events.OrderPaid, fmt.Sprintf("S%d", i), fmt.Sprintf("order:%d:paid", i), json.RawMessage(`{}`))
		}); err != nil {
			t.Fatal(err)
		}
	}
	cq := &captureQueue{}
	relay := NewOutboxRelay(d, cq, slog.New(slog.DiscardHandler))

	relay.tick(ctx) // 首轮全部投递
	if len(cq.tasks) != 3 {
		t.Fatalf("投递数量：%d，期望 3", len(cq.tasks))
	}
	// 任务类型与载荷
	if cq.tasks[0].Type != "event:"+events.OrderPaid {
		t.Errorf("任务类型 = %s", cq.tasks[0].Type)
	}
	var env events.Envelope
	if err := json.Unmarshal(cq.tasks[0].Payload, &env); err != nil {
		t.Fatal(err)
	}
	if env.EventID == 0 || env.Type != events.OrderPaid {
		t.Errorf("信封字段异常：%+v", env)
	}
	// 已 published 不再重复投递
	relay.tick(ctx)
	if len(cq.tasks) != 3 {
		t.Fatalf("重复投递：%d", len(cq.tasks))
	}
	published, _ := d.Client.OutboxEvent.Query().Where(outboxevent.StatusEQ(outboxevent.StatusPublished)).Count(ctx)
	if published != 3 {
		t.Fatalf("published 标记：%d", published)
	}
}

func TestRelayRetryThenFailed(t *testing.T) {
	d := newTestData(t)
	w := NewOutboxWriter(d)
	ctx := context.Background()
	_ = Tx(ctx, d, func(ctx context.Context) error {
		return w.Write(ctx, "order", events.OrderPaid, "S9", "order:9:paid", json.RawMessage(`{}`))
	})
	cq := &captureQueue{}
	cq.fail.Store(1)
	relay := NewOutboxRelay(d, cq, slog.New(slog.DiscardHandler))
	for i := 0; i < relayMaxTry; i++ {
		relay.tick(ctx)
	}
	failed, _ := d.Client.OutboxEvent.Query().Where(outboxevent.StatusEQ(outboxevent.StatusFailed)).Count(ctx)
	if failed != 1 {
		t.Fatalf("连续失败应置 failed：%d", failed)
	}
}

func TestConsumerIdempotent(t *testing.T) {
	d := newTestData(t)
	dp := NewDispatcher(d, slog.New(slog.DiscardHandler))
	var calls atomic.Int32
	dp.Register(HandlerReg{
		Consumer: "test.fulfillment",
		Type:     events.OrderPaid,
		Fn:       func(context.Context, events.Envelope) error { calls.Add(1); return nil },
	})
	env := events.Envelope{EventID: 42, Type: events.OrderPaid, AggregateID: "S42"}
	for i := 0; i < 5; i++ { // 重复投递 5 次
		if err := dp.Dispatch(context.Background(), env); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("消费幂等失败：执行 %d 次，期望 1", got)
	}
	// 处理器报错：processed_events 已记录（M0 语义：错误返回但不重复执行），文档已注明
}

func TestSyncQueueDeadLetter(t *testing.T) {
	d := newTestData(t)
	dead := NewFailedTaskWriter(d)
	sq := &queue.SyncQueue{
		Log:     slog.New(slog.DiscardHandler),
		Handler: func(context.Context, queue.Task) error { return errors.New("处理失败") },
		Dead:    dead,
	}
	if err := sq.Enqueue(context.Background(), queue.Task{Type: "event:" + events.OrderPaid, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		n, _ := d.Client.FailedTask.Query().Count(context.Background())
		if n == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("死信未落 failed_tasks（2s 内）")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSyncQueueHappyPath(t *testing.T) {
	d := newTestData(t)
	dp := NewDispatcher(d, slog.New(slog.DiscardHandler))
	var calls atomic.Int32
	dp.Register(HandlerReg{Consumer: "t.h", Type: events.OrderCreated, Fn: func(context.Context, events.Envelope) error {
		calls.Add(1)
		return nil
	}})
	sq := &queue.SyncQueue{Log: slog.New(slog.DiscardHandler), Handler: dp.HandleTask, Dead: NewFailedTaskWriter(d)}
	env, _ := json.Marshal(events.Envelope{EventID: 7, Type: events.OrderCreated})
	if err := sq.Enqueue(context.Background(), queue.Task{Type: "event:" + events.OrderCreated, Payload: env}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatal("SyncQueue 未分发到处理器")
	}
}

func TestDispatcherBadPayload(t *testing.T) {
	d := newTestData(t)
	dp := NewDispatcher(d, slog.New(slog.DiscardHandler))
	if err := dp.HandleTask(context.Background(), queue.Task{Type: "event:x", Payload: []byte(`not-json`)}); err == nil {
		t.Fatal("非法载荷应报错")
	}
}

func TestDispatcherRegisterReplace(t *testing.T) {
	d := newTestData(t)
	dp := NewDispatcher(d, slog.New(slog.DiscardHandler))
	var first, second atomic.Int32
	reg := func(f *atomic.Int32) HandlerReg {
		return HandlerReg{Consumer: "same.consumer", Type: events.OrderCanceled, Fn: func(context.Context, events.Envelope) error {
			f.Add(1)
			return nil
		}}
	}
	dp.Register(reg(&first))
	dp.Register(reg(&second)) // 同 Consumer 覆盖
	if err := dp.Dispatch(context.Background(), events.Envelope{EventID: 1, Type: events.OrderCanceled}); err != nil {
		t.Fatal(err)
	}
	if first.Load() != 0 || second.Load() != 1 {
		t.Fatalf("覆盖注册失效：first=%d second=%d", first.Load(), second.Load())
	}
	// 非法注册被忽略
	dp.Register(HandlerReg{Consumer: "", Type: "x", Fn: func(context.Context, events.Envelope) error { return nil }})
}

func TestRelayEmptyTick(t *testing.T) {
	d := newTestData(t)
	relay := NewOutboxRelay(d, &captureQueue{}, slog.New(slog.DiscardHandler))
	if err := relay.tick(context.Background()); err != nil {
		t.Fatalf("空扫描应无错：%v", err)
	}
}
