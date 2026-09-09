package queue

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCronIsolatesAndCancelsTasks(t *testing.T) {
	c := NewCron()
	defer c.Stop()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	fast := make(chan struct{}, 4)
	var calls atomic.Int32
	c.AddEvery("a.blocked", time.Second, func(ctx context.Context) { calls.Add(1); close(started); <-ctx.Done(); <-release; close(done) })
	c.AddEvery("order.expire", time.Second, func(context.Context) { fast <- struct{}{} })
	c.fireDue(time.Now().Add(2*time.Second), nil)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task not started")
	}
	select {
	case <-fast:
	case <-time.After(time.Second):
		t.Fatal("expiry blocked by another task")
	}
	c.fireDue(time.Now().Add(4*time.Second), nil)
	if calls.Load() != 1 {
		t.Fatal("overlapping execution")
	}
	c.Stop()
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel task context")
	}
}
