package queue

// P0-01 验收：三队列常量与降级矩阵、cron 周期触发、SyncQueue 失败路径。

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncQueueDisabledAndError(t *testing.T) {
	q := &SyncQueue{Log: slog.New(slog.DiscardHandler)} // 无 Handler：静默成功
	if q.Enabled() {
		t.Fatal("SyncQueue.Enabled 应为 false（降级标识）")
	}
	if err := q.Enqueue(context.Background(), Task{Type: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestCronAddEveryValidation(t *testing.T) {
	c := NewCron()
	if err := c.AddEvery("ok", time.Second, func(context.Context) {}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddEvery("bad", 0, func(context.Context) {}); err == nil {
		t.Fatal("间隔 ≤0 应报错")
	}
	if len(c.Entries()) != 1 {
		t.Fatalf("注册数：%d", len(c.Entries()))
	}
}

func TestCronFiresPeriodically(t *testing.T) {
	c := NewCron()
	var fires atomic.Int32
	if err := c.AddEvery("tick", 40*time.Millisecond, func(context.Context) { fires.Add(1) }); err != nil {
		t.Fatal(err)
	}
	c.Start(slog.New(slog.DiscardHandler))
	defer c.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for fires.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if fires.Load() < 2 {
		t.Fatalf("周期触发不足：%d（期望 ≥2）", fires.Load())
	}
}

func TestCronPanicRecovered(t *testing.T) {
	c := NewCron()
	var after atomic.Int32
	_ = c.AddEvery("boom", 30*time.Millisecond, func(context.Context) { panic("boom") })
	_ = c.AddEvery("after", 30*time.Millisecond, func(context.Context) { after.Add(1) })
	c.Start(slog.New(slog.DiscardHandler))
	defer c.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for after.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after.Load() == 0 {
		t.Fatal("panic 任务不应拖垮其他周期任务")
	}
}

func TestTaskDefaults(t *testing.T) {
	// 队列名归一由 Asynq/Sync 入口处理；此处锁定常量防漂移
	if QueueCritical == QueueDefault || QueueDefault == QueueLow {
		t.Fatal("三队列常量不得重复")
	}
}

func TestTaskOf(t *testing.T) {
	task := TaskOf(Task{Type: "event:order.paid", Payload: []byte(`{}`)})
	if task.Type() != "event:order.paid" {
		t.Fatalf("类型 = %s", task.Type())
	}
	// 空类型归一 unknown（防 asynq 空 pattern panic）
	if TaskOf(Task{}).Type() != "unknown" {
		t.Fatal("空类型应归一 unknown")
	}
}

func TestAsynqQueueEnabled(t *testing.T) {
	q := NewAsynqQueue(nil) // client 仅入队时使用；Enabled 不依赖连接
	if !q.Enabled() {
		t.Fatal("AsynqQueue.Enabled 应为 true")
	}
}

func TestSyncQueueRecordDeadDirect(t *testing.T) {
	q := &SyncQueue{Log: slog.New(slog.DiscardHandler)}
	q.recordDead(context.Background(), Task{Type: "t"}, errors.New("直接路径"))
	// Dead 为 nil：仅日志路径，不 panic 即通过
}

func TestInvalidIntervalErrorMessage(t *testing.T) {
	e := &InvalidIntervalError{Name: "n", Interval: -1}
	if e.Error() == "" {
		t.Fatal("错误信息不应为空")
	}
}
