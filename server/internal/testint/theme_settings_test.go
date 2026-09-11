//go:build integration

package testint

import (
	"context"
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"sync"
	"testing"
)

func TestThemeSettingsConcurrentPublish(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T) *Harness
	}{{"mysql", MySQL}, {"postgres", PG}} {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.open(t)
			r := settings.NewRepoImpl(h.Data)
			ctx := context.Background()
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for _, v := range []string{"one", "two"} {
				wg.Add(1)
				go func(v string) {
					defer wg.Done()
					results <- r.PutThemeState(ctx, "0:classic", "", json.RawMessage(`{"revision":"`+v+`"}`), nil)
				}(v)
			}
			wg.Wait()
			close(results)
			wins := 0
			for err := range results {
				if err == nil {
					wins++
				}
			}
			if wins != 1 {
				t.Fatalf("expected one publication, got %d", wins)
			}
		})
	}
}
