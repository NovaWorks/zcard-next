package orderaccess

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"testing"
	"time"
)

type testGate struct{ failures map[string]int }

func (g *testGate) FetchPasswordState(_ context.Context, key string) (State, error) {
	return State{Failures: g.failures[key], ExpiresAt: time.Now().Add(LockTTL)}, nil
}
func (g *testGate) RecordFetchFailure(ctx context.Context, key string) (State, error) {
	g.failures[key]++
	return g.FetchPasswordState(ctx, key)
}
func (g *testGate) ResetFetchFailures(_ context.Context, key string) error {
	delete(g.failures, key)
	return nil
}
func TestPasswordFailureLockSharedAcrossPorts(t *testing.T) {
	ctx := context.Background()
	hash, err := crypto.HashPassword("1234")
	if err != nil {
		t.Fatal(err)
	}
	gate := &testGate{failures: map[string]int{}}
	for _, ip := range []string{"127.0.0.1:1001", "127.0.0.1:1002", "127.0.0.1:1003", "127.0.0.1:1004", "127.0.0.1:1005"} {
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
