//go:build unix

package pluginstorage

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

type Lock struct{ file *os.File }

func Acquire(root string) (*Lock, error)       { return acquire(root, unix.LOCK_EX) }
func AcquireShared(root string) (*Lock, error) { return acquire(root, unix.LOCK_SH) }
func acquire(root string, mode int) (*Lock, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, ".plugin-instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), mode|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{file: f}, nil
}
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	err := l.file.Close()
	l.file = nil
	return err
}
