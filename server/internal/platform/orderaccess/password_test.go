package orderaccess

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"testing"
)

type testGate struct{ failures map[string]int }

func (g *testGate) IsLocked(_ context.Context, key string) (bool, error) {
	return g.failures[key] >= 2, nil
}
func (g *testGate) LockFetchFailure(_ context.Context, key string) error {
	g.failures[key]++
	return nil
}
func TestPasswordFailureLockSharedAcrossPorts(t *testing.T) {
	ctx := context.Background()
	hash, err := crypto.HashPassword("1234")
	if err != nil {
		t.Fatal(err)
	}
	gate := &testGate{failures: map[string]int{}}
	for _, ip := range []string{"127.0.0.1:1001", "127.0.0.1:1002"} {
		if Verify(ctx, gate, "ORDER", hash, "wrong", ip) == nil {
			t.Fatal("bad password accepted")
		}
	}
	if Verify(ctx, gate, "ORDER", hash, "1234", "127.0.0.1:1003") == nil {
		t.Fatal("changing remote port bypassed lock")
	}
	if Verify(ctx, gate, "OTHER", hash, "1234", "127.0.0.1:1003") != nil {
		t.Fatal("unrelated order locked")
	}
	if Verify(ctx, gate, "ORDER", hash, "1234", "127.0.0.2:1003") != nil {
		t.Fatal("unrelated peer locked")
	}
}
