// Package pluginstorage owns instance-local persistence paths and host keys.
package pluginstorage

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrLocked = errors.New("plugin instance is already running")

func ResolveRoot(configDir, configured string) (string, error) {
	base, err := filepath.Abs(configDir)
	if err != nil {
		return "", err
	}
	if configured == "" {
		configured = os.Getenv("ZCARD_PLUGIN_DATA_DIR")
	}
	if configured == "" {
		configured = filepath.Join(base, "..", "data")
	}
	if !filepath.IsAbs(configured) {
		configured = filepath.Join(base, configured)
	}
	return filepath.Clean(configured), nil
}

// EnsureKey never replaces a missing key referenced by orders: callers must
// check existing fingerprints before allowing first initialization.
func EnsureKey(root string, allowCreate bool) ([]byte, error) {
	dir := filepath.Join(root, "secrets", "order-idempotency")
	path := filepath.Join(dir, "v1.key")
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() != 32 {
			return nil, fmt.Errorf("invalid idempotency key file or permissions")
		}
		return os.ReadFile(path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if !allowCreate {
		return nil, fmt.Errorf("idempotency key missing for existing fingerprints; restore backup")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, ".key-")
	if err != nil {
		return nil, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(key); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	// Hard-link publishes without overwriting an existing key.
	if err = os.Link(temp, path); err != nil {
		if os.IsExist(err) {
			return EnsureKey(root, false)
		}
		return nil, err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err = parent.Sync(); err != nil {
		return nil, err
	}
	return key, nil
}
