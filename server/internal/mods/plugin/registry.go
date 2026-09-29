package plugin

import (
	"context"
	"sync"

	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
)

type runtimeSlot struct {
	runtime    port.PreparedRuntime
	generation uint64
	refs       int
	retired    bool
}

// Coordinator serializes lifecycle and rule writes. Purchases pin leases before
// entering product transactions, so rule saves never reverse the lock order.
type Coordinator struct {
	licenseCheck func(port.PreparedRuntime) bool
	mu           sync.Mutex
	readOnly     bool
	draining     int
	slots        map[string]*runtimeSlot
	pending      map[string]bool
	observed     map[string]uint64
}

func NewCoordinator() *Coordinator {
	return &Coordinator{slots: map[string]*runtimeSlot{}, pending: map[string]bool{}, observed: map[string]uint64{}}
}
func (c *Coordinator) Acquire(ctx context.Context, id string) (port.RuntimeLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	slot := c.slots[id]
	if slot == nil {
		return nil, contractError(pc.Unavailable, "runtime unavailable")
	}
	if c.licenseCheck != nil && !c.licenseCheck(slot.runtime) {
		return nil, contractError(pc.Unavailable, "plugin entitlement unavailable")
	}
	slot.refs++
	return &lease{owner: c, slot: slot}, nil
}

type lease struct {
	owner *Coordinator
	slot  *runtimeSlot
	once  sync.Once
}

func (l *lease) Generation() uint64 { return l.slot.generation }
func (l *lease) Evaluate(ctx context.Context, in pc.Input) (pc.Decision, error) {
	return l.slot.runtime.Evaluate(ctx, in)
}
func (l *lease) Release() {
	l.once.Do(func() {
		l.owner.mu.Lock()
		l.slot.refs--
		closeNow := l.slot.refs == 0 && l.slot.retired
		if closeNow {
			l.owner.draining--
		}
		l.owner.mu.Unlock()
		if closeNow {
			_ = l.slot.runtime.Close()
		}
	})
}

// publishLocked returns a drained old runtime for closing outside the lock.
func (c *Coordinator) publishLocked(id string, generation uint64, rt port.PreparedRuntime) port.PreparedRuntime {
	c.observed[id] = generation
	old := c.slots[id]
	delete(c.slots, id)
	if rt != nil {
		c.slots[id] = &runtimeSlot{runtime: rt, generation: generation}
	}
	if old != nil {
		old.retired = true
		if old.refs > 0 {
			c.draining++
		}
		if old.refs == 0 {
			return old.runtime
		}
	}
	return nil
}

var _ port.RegistrySnapshot = (*Coordinator)(nil)
