package pluginstorage

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

type Lock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func Acquire(root string) (*Lock, error)       { return acquire(root, windows.LOCKFILE_EXCLUSIVE_LOCK) }
func AcquireShared(root string) (*Lock, error) { return acquire(root, 0) }
func acquire(root string, mode uint32) (*Lock, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, ".plugin-instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	l := &Lock{file: f}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), mode|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &l.overlapped); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return l, nil
}
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	err := l.file.Close()
	l.file = nil
	return err
}
