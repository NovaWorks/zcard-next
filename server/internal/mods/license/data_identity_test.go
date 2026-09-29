package license

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

type unavailableSettings struct{ writes int }

func (*unavailableSettings) Get(context.Context, string, string) (json.RawMessage, error) {
	return nil, errors.New("database offline")
}
func (s *unavailableSettings) Put(context.Context, string, string, json.RawMessage) error {
	s.writes++
	return nil
}

func TestInstanceIdentityReadFailureDoesNotReplace(t *testing.T) {
	s := &unavailableSettings{}
	if _, err := NewLicenseRepo(s).InstanceID(context.Background()); err == nil || s.writes != 0 {
		t.Fatal("failed read replaced identity")
	}
	bad := &fakeSettingsStore{m: map[string]json.RawMessage{"license/instance_id": json.RawMessage(`{"invalid":true}`)}}
	if _, err := NewLicenseRepo(bad).InstanceID(context.Background()); err == nil {
		t.Fatal("corrupt identity silently replaced")
	}
}
func TestConcurrentInstanceIdentityIsStable(t *testing.T) {
	r := NewLicenseRepo(&fakeSettingsStore{m: map[string]json.RawMessage{}})
	var wg sync.WaitGroup
	ids := make(chan string, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := r.InstanceID(context.Background())
			if err != nil {
				t.Error(err)
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("multiple identities")
		}
	}
}
