package adapter

import (
	"context"
	"time"
)

type requestPacingKey struct{}
type requestPacing struct {
	gate     chan struct{}
	interval time.Duration
	next     time.Time
}

// WithRequestInterval spaces maintenance reads, including retries and per-SKU
// quotes. The context is shared across products; purchase requests never use it.
func WithRequestInterval(ctx context.Context, interval time.Duration) context.Context {
	if interval <= 0 {
		return ctx
	}
	p := &requestPacing{gate: make(chan struct{}, 1), interval: interval}
	p.gate <- struct{}{}
	return context.WithValue(ctx, requestPacingKey{}, p)
}

func waitRequestInterval(ctx context.Context) error {
	p, _ := ctx.Value(requestPacingKey{}).(*requestPacing)
	if p == nil {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.gate:
	}
	defer func() { p.gate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay := time.Until(p.next); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	p.next = time.Now().Add(p.interval)
	return nil
}
