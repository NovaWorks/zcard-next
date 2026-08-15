package queue

// 进程内周期任务（无 Redis 时的 cron 兜底，ADR-D6；有 Redis 时由 asynq Scheduler 承接，M1 评估切换）。
// 采用固定间隔注册（AddEvery）而非 cron 表达式——M0 周期任务均为等间隔扫描型；
// 多实例部署纪律：进程内 cron 仅在 worker/all 模式运行（多实例 = 需单 worker）。

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Cron 进程内定时器（并发安全；panic 恢复防单任务拖垮循环）。
type Cron struct {
	mu      sync.Mutex
	entries map[string]cronEntry
	stop    chan struct{}
	stopped sync.Once
	started bool
}

type cronEntry struct {
	name     string
	interval time.Duration
	fn       func(context.Context)
	next     time.Time
}

// NewCron 构造。
func NewCron() *Cron {
	return &Cron{entries: map[string]cronEntry{}, stop: make(chan struct{})}
}

// AddEvery 注册周期任务（name 唯一，重复注册覆盖——测试与重装配友好）。
// interval ≤0 拒绝注册（返回错误由调用方决定是否致命）。
func (c *Cron) AddEvery(name string, interval time.Duration, fn func(context.Context)) error {
	if interval <= 0 {
		return errInvalidInterval(name, interval)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[name] = cronEntry{name: name, interval: interval, fn: fn, next: time.Now().Add(interval)}
	return nil
}

// Start 启动调度循环（500ms 粒度扫描；幂等：重复 Start 无效）。
func (c *Cron) Start(log *slog.Logger) {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.mu.Unlock()
	go c.loop(log)
}

// Stop 停止（幂等）。
func (c *Cron) Stop() {
	c.stopped.Do(func() { close(c.stop) })
}

func (c *Cron) loop(log *slog.Logger) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case now := <-ticker.C:
			c.fireDue(now, log)
		}
	}
}

func (c *Cron) fireDue(now time.Time, log *slog.Logger) {
	c.mu.Lock()
	due := make([]cronEntry, 0, len(c.entries))
	for _, e := range c.entries {
		if !now.Before(e.next) {
			due = append(due, e)
			ne := e
			ne.next = now.Add(ne.interval)
			c.entries[e.name] = ne
		}
	}
	c.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].name < due[j].name })
	for _, e := range due {
		c.safeRun(e, log)
	}
}

func (c *Cron) safeRun(e cronEntry, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			if log != nil {
				log.Error("queue.cron.panic", "task", e.name, "panic", r)
			}
		}
	}()
	e.fn(context.Background())
}

// Entries 快照（测试与诊断）。
func (c *Cron) Entries() map[string]time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]time.Duration, len(c.entries))
	for k, e := range c.entries {
		out[k] = e.interval
	}
	return out
}

func errInvalidInterval(name string, d time.Duration) error {
	return &InvalidIntervalError{Name: name, Interval: d}
}

// InvalidIntervalError 非法间隔。
type InvalidIntervalError struct {
	Name     string
	Interval time.Duration
}

func (e *InvalidIntervalError) Error() string {
	return "queue: cron 任务 " + e.Name + " 间隔非法（≤0）"
}
