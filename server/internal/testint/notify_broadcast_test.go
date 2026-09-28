//go:build integration

package testint

import (
	"context"
	"fmt"
	"testing"
	"time"

	entbase "entgo.io/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/notify"
)

func TestBroadcastTransitionsMySQL(t *testing.T) { runBroadcastTransitions(t, MySQL) }
func TestBroadcastTransitionsPG(t *testing.T)    { runBroadcastTransitions(t, PG) }

func runBroadcastTransitions(t *testing.T, harness func(*testing.T) *Harness) {
	for _, cancelSecond := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelSecond), func(t *testing.T) {
			h := harness(t)
			r := notify.NewNotifyRepo(h.Data)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			u := h.Data.Client.User.Create().SetUsername("recipient").SaveX(ctx)
			b, err := r.CreateBroadcast(ctx, notify.BroadcastInput{Title: "once", Content: "one message", Channels: []string{"inbox"}, TargetType: "specified", TargetIDs: []uint64{u.ID}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			ready, release := make(chan struct{}, 2), make(chan struct{})
			h.Data.Client.NotifyBroadcast.Use(func(next entbase.Mutator) entbase.Mutator {
				return entbase.MutateFunc(func(ctx context.Context, m entbase.Mutation) (entbase.Value, error) {
					if status, ok := m.Field("status"); ok && (fmt.Sprint(status) == "sending" || fmt.Sprint(status) == "canceled") {
						ready <- struct{}{}
						select {
						case <-release:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					return next.Mutate(ctx, m)
				})
			})
			svc := notify.NewBroadcastService(r, notify.NewDispatcher(r, notify.NewInboxChannel(r)), nil)
			results := make(chan error, 2)
			go func() { results <- svc.Execute(ctx, b.ID) }()
			go func() {
				if cancelSecond {
					_, err := svc.Cancel(ctx, b.ID)
					results <- err
				} else {
					results <- svc.Execute(ctx, b.ID)
				}
			}()
			for range 2 {
				select {
				case <-ready:
				case <-ctx.Done():
					<-results
					<-results
					t.Fatal("callers did not reach the pending transition")
				}
			}
			close(release)
			for range 2 {
				if err := <-results; err != nil && !(cancelSecond && err == notify.ErrBroadcastStarted) {
					t.Error(err)
				}
			}
			fin, err := r.GetBroadcast(ctx, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if cancelSecond && string(fin.Status) == "canceled" {
				want = 0
			} else if string(fin.Status) != "done" {
				t.Fatalf("unexpected final status: %s", fin.Status)
			}
			if count, err := r.UnreadCount(ctx, u.ID); err != nil || int64(count) != want || fin.SentCount != want || fin.FailedCount != 0 {
				t.Fatalf("count=%d want=%d broadcast=%+v err=%v", count, want, fin, err)
			}
		})
	}
}
