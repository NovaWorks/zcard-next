package pluginstorage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceLockAndKeyRecovery(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if other, e := Acquire(root); !errors.Is(e, ErrLocked) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("second writer: %v", e)
	}
	if other, e := AcquireShared(root); !errors.Is(e, ErrLocked) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("shared reader bypassed writer: %v", e)
	}
	key, err := EnsureKey(root, true)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EnsureKey(root, false)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatal("key rotated")
	}
	if err = os.Remove(filepath.Join(root, "secrets", "order-idempotency", "v1.key")); err != nil {
		t.Fatal(err)
	}
	if _, err = EnsureKey(root, false); err == nil {
		t.Fatal("missing referenced key recreated")
	}
	lock.Close()
	shared, err := AcquireShared(root)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	shared2, err := AcquireShared(root)
	if err != nil {
		t.Fatal(err)
	}
	defer shared2.Close()
	if other, e := Acquire(root); !errors.Is(e, ErrLocked) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("writer bypassed shared reader: %v", e)
	}
}
func TestPathsDoNotDependOnWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	got, err := ResolveRoot(filepath.Join(base, "configs"), "../persistent")
	if err != nil || got != filepath.Join(base, "persistent") {
		t.Fatalf("root=%s err=%v", got, err)
	}
}
