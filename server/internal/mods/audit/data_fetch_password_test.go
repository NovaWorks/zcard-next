package audit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/orderaccess"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

func TestFetchPasswordFiveFailuresAndRecovery(t *testing.T) {
	r, d := newAuditRepo(t)
	ctx := context.Background()
	hash, err := crypto.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	verify := func(p string) error { return orderaccess.Verify(ctx, r, "ORDER", hash, p, "127.0.0.1:1234") }
	key := "fetch:127.0.0.1:ORDER"
	for i := 0; i < 6; i++ {
		if e := kerrors.FromError(verify("")); e.Code != 400 {
			t.Fatal(e)
		}
	}
	if s, _ := r.FetchPasswordState(ctx, key); s.Failures != 0 {
		t.Fatal("missing credentials consumed attempts", s)
	}
	for i := 1; i <= 4; i++ {
		e := kerrors.FromError(verify("wrong"))
		if e.Code != 404 {
			t.Fatalf("attempt %d: %v", i, e)
		}
		state, err := r.FetchPasswordState(ctx, key)
		if err != nil || state.Failures != i || state.Locked() {
			t.Fatalf("attempt %d: %+v %v", i, state, err)
		}
	}
	// Success before the fifth failure clears the consecutive count.
	if err := verify("correct-password"); err != nil {
		t.Fatal(err)
	}
	if s, _ := r.FetchPasswordState(ctx, key); s.Failures != 0 {
		t.Fatal(s)
	}
	for i := 1; i <= 5; i++ {
		e := kerrors.FromError(verify("wrong"))
		want := int32(404)
		if i == 5 {
			want = 429
		}
		if e.Code != want {
			t.Fatalf("attempt %d: %v", i, e)
		}
	}
	state, _ := r.FetchPasswordState(ctx, key)
	if !state.Locked() || time.Until(state.ExpiresAt) < 29*time.Minute {
		t.Fatal(state)
	}
	// Reconstructing the repository (as on restart) retains the lock and deadline.
	r = NewAuditRepo(d, testLogger())
	e := kerrors.FromError(verify("correct-password"))
	if e.Code != 429 || e.Reason != "order.FETCH_LOCKED" || e.Metadata["remaining_attempts"] != "0" {
		t.Fatal(e)
	}
	if err := r.ResetFetchFailures(ctx, key); err != nil {
		t.Fatal(err)
	}
	again, _ := r.RecordFetchFailure(ctx, key)
	if !again.ExpiresAt.Equal(state.ExpiresAt) {
		t.Fatal("locked request extended expiry")
	}
	if _, err := d.DB.ExecContext(ctx, "UPDATE risk_lock_keys SET expires_at = datetime('now', '-1 minute')"); err != nil {
		t.Fatal(err)
	}
	// Expired row still exists, but the next failure starts at one.
	if e := kerrors.FromError(verify("wrong")); e.Metadata["remaining_attempts"] != "4" {
		t.Fatal(e)
	}
	if err := verify("correct-password"); err != nil {
		t.Fatal(err)
	}
	// A fresh cycle can lock again without waiting for cleanup.
	for i := 0; i < 5; i++ {
		_, err = r.RecordFetchFailure(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
	}
	if s, _ := r.FetchPasswordState(ctx, key); !s.Locked() {
		t.Fatal(s)
	}
}

func TestFetchPasswordCountersSharedAcrossRepositories(t *testing.T) {
	r, d := newAuditRepo(t)
	ctx := context.Background()
	key := "fetch:127.0.0.1:CONCURRENT"
	if s, err := r.RecordFetchFailure(ctx, key); err != nil || s.Failures != 1 {
		t.Fatal(s, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			repo := NewAuditRepo(d, testLogger())
			if _, err := repo.RecordFetchFailure(ctx, key); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	s, err := r.FetchPasswordState(ctx, key)
	if err != nil || s.Failures != 5 || !s.Locked() {
		t.Fatal(s, err)
	}
	for _, other := range []string{"fetch:127.0.0.2:CONCURRENT", "fetch:127.0.0.1:OTHER"} {
		if s, _ := r.FetchPasswordState(ctx, other); s.Failures != 0 {
			t.Fatal("unrelated key affected", s)
		}
	}
}
