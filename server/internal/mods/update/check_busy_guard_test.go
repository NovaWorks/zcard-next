package update

import (
	"context"
	"testing"
)

// TestCheckBusyGuard 更新进行中 Check 必须拒绝重入(不打断状态机)。
func TestCheckBusyGuard(t *testing.T) {
	s := &Service{}
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	if _, err := s.Check(context.Background()); err != ErrBusy {
		t.Fatalf("busy 时 Check 应返回 ErrBusy,得到 %v", err)
	}
	// target 在途(非 busy,如等待恢复期)同样拒绝
	s.mu.Lock()
	s.busy = false
	s.st.Target = "v9.9.9"
	s.mu.Unlock()
	if _, err := s.Check(context.Background()); err != ErrBusy {
		t.Fatalf("target 在途时 Check 应返回 ErrBusy,得到 %v", err)
	}
}
