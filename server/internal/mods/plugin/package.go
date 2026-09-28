package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
)

var pluginIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FilePackages contains only signed immutable artifacts. No runtime is invoked.
type FilePackages struct {
	root string
	keys map[string]ed25519.PublicKey
	host pc.Host
	mu   sync.Mutex
}

func NewFilePackages(root string, keys map[string]ed25519.PublicKey, host pc.Host) (*FilePackages, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("plugin data root must be a real directory")
	}
	copied := make(map[string]ed25519.PublicKey, len(keys))
	for k, v := range keys {
		if len(v) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid plugin trust key")
		}
		copied[k] = append(ed25519.PublicKey(nil), v...)
	}
	return &FilePackages{root: abs, keys: copied, host: host}, nil
}
func digest(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }
func contractError(code pc.ErrorCode, detail string) error {
	return &pc.Error{Code: code, Detail: detail}
}
func (s *FilePackages) verify(desc, sig, archive []byte) (port.Artifact, map[string][]byte, error) {
	var a port.Artifact
	if err := pc.Validate(pc.ArtifactKind, desc); err != nil {
		return a, nil, err
	}
	if err := json.Unmarshal(desc, &a.Descriptor); err != nil {
		return a, nil, err
	}
	msg, err := pc.SigningMessage(a.Descriptor)
	if err != nil {
		return a, nil, err
	}
	key, ok := s.keys[a.Descriptor.KeyID]
	if !ok || len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, msg, sig) {
		return a, nil, contractError(pc.InvalidContract, "untrusted or invalid signature")
	}
	n, _ := strconv.ParseUint(string(a.Descriptor.ArchiveBytes), 10, 64)
	if uint64(len(archive)) != n || digest(archive) != a.Descriptor.ArchiveSHA256 {
		return a, nil, contractError(pc.InvalidContract, "archive digest or size mismatch")
	}
	z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return a, nil, contractError(pc.InvalidContract, "invalid ZIP")
	}
	if len(z.File) != 2 {
		return a, nil, contractError(pc.InvalidContract, "v1 ZIP requires exactly manifest.json and main.js")
	}
	files := make(map[string][]byte)
	total := 0
	for _, f := range z.File {
		limit := pc.MaxScriptBytes
		if f.Name == "manifest.json" {
			limit = pc.MaxJSONBytes
		} else if f.Name != "main.js" {
			return a, nil, contractError(pc.InvalidContract, "unexpected ZIP entry")
		}
		if !f.Mode().IsRegular() || f.Flags&1 != 0 || files[f.Name] != nil || f.UncompressedSize64 > uint64(limit) {
			return a, nil, contractError(pc.InvalidContract, "unsafe or oversized ZIP entry")
		}
		r, err := f.Open()
		if err != nil {
			return a, nil, err
		}
		b, err := io.ReadAll(io.LimitReader(r, int64(limit+1)))
		closeErr := r.Close()
		if err != nil {
			return a, nil, err
		}
		if closeErr != nil {
			return a, nil, closeErr
		}
		total += len(b)
		if len(b) > limit || total > pc.MaxExpandedBytes {
			return a, nil, contractError(pc.InvalidContract, "expanded ZIP exceeds limit")
		}
		files[f.Name] = b
	}
	if digest(files["manifest.json"]) != a.Descriptor.ManifestSHA256 {
		return a, nil, contractError(pc.InvalidContract, "manifest digest mismatch")
	}
	if err = pc.Validate(pc.ManifestKind, files["manifest.json"]); err != nil {
		return a, nil, err
	}
	if err = json.Unmarshal(files["manifest.json"], &a.Manifest); err != nil {
		return a, nil, err
	}
	if a.Manifest.ID != a.Descriptor.PluginID || a.Manifest.Version != a.Descriptor.Version {
		return a, nil, contractError(pc.InvalidContract, "descriptor identity mismatch")
	}
	if _, err = pc.CheckCompatibility(a.Manifest, s.host); err != nil {
		return a, nil, err
	}
	return a, files, nil
}
func (s *FilePackages) Stage(ctx context.Context, desc, sig []byte, r io.Reader) (port.Artifact, error) {
	var empty port.Artifact
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	// Authenticate descriptor before consuming the archive stream.
	if err := pc.Validate(pc.ArtifactKind, desc); err != nil {
		return empty, err
	}
	var d pc.ArtifactDescriptor
	if err := json.Unmarshal(desc, &d); err != nil {
		return empty, err
	}
	msg, err := pc.SigningMessage(d)
	if err != nil {
		return empty, err
	}
	key, ok := s.keys[d.KeyID]
	if !ok || len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, msg, sig) {
		return empty, contractError(pc.InvalidContract, "untrusted or invalid signature")
	}
	b, err := io.ReadAll(io.LimitReader(r, pc.MaxArchiveBytes+1))
	if err != nil {
		return empty, err
	}
	a, files, err := s.verify(desc, sig, b)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, e := os.ReadDir(s.root)
	if e != nil {
		return empty, e
	}
	count := 0
	exists := false
	for _, entry := range entries {
		if entry.IsDir() && pluginIDPattern.MatchString(entry.Name()) {
			count++
			if entry.Name() == a.Manifest.ID {
				exists = true
			}
		}
	}
	if !exists && count >= 128 {
		return empty, contractError(pc.Unavailable, "installed artifact namespace limit reached")
	}
	parent := filepath.Join(s.root, a.Manifest.ID)
	if err = os.MkdirAll(parent, 0700); err != nil {
		return empty, err
	}
	st, err := os.Lstat(parent)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return empty, fmt.Errorf("unsafe artifact directory")
	}
	target := filepath.Join(parent, a.Descriptor.ArchiveSHA256)
	if _, err = os.Lstat(target); err == nil {
		existing, _, e := s.read(target)
		return existing, e
	} else if !os.IsNotExist(err) {
		return empty, err
	}
	versions, e := os.ReadDir(parent)
	if e != nil {
		return empty, e
	}
	count = 0
	for _, v := range versions {
		if v.IsDir() && digestPattern.MatchString(v.Name()) {
			count++
		}
	}
	if count >= 8 {
		return empty, contractError(pc.Unavailable, "retained artifact limit reached; drain in-flight requests first")
	}
	temp, err := os.MkdirTemp(parent, ".staging-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(temp)
	files["archive.zip"] = b
	files["descriptor.json"] = desc
	files["signature.ed25519"] = sig
	for name, content := range files {
		if err = writeSynced(filepath.Join(temp, name), content); err != nil {
			return empty, err
		}
	}
	if err = syncDir(temp); err != nil {
		return empty, err
	}
	if err = os.Rename(temp, target); err != nil {
		return empty, err
	}
	if err = syncDir(parent); err != nil {
		return empty, err
	}
	return a, nil
}
func writeSynced(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func readLimitedFile(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("unsafe artifact file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("artifact file exceeds limit")
	}
	return b, err
}
func (s *FilePackages) read(path string) (port.Artifact, []byte, error) {
	var a port.Artifact
	st, err := os.Lstat(path)
	if err != nil {
		return a, nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return a, nil, fmt.Errorf("unsafe artifact directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return a, nil, err
	}
	defer root.Close()
	desc, err := readLimitedFile(root, "descriptor.json", pc.MaxJSONBytes)
	if err != nil {
		return a, nil, err
	}
	sig, err := readLimitedFile(root, "signature.ed25519", ed25519.SignatureSize)
	if err != nil {
		return a, nil, err
	}
	raw, err := readLimitedFile(root, "archive.zip", pc.MaxArchiveBytes)
	if err != nil {
		return a, nil, err
	}
	a, _, err = s.verify(desc, sig, raw)
	if err == nil && (filepath.Base(path) != a.Descriptor.ArchiveSHA256 || filepath.Base(filepath.Dir(path)) != a.Manifest.ID) {
		err = fmt.Errorf("artifact path identity mismatch")
	}
	return a, raw, err
}
func (s *FilePackages) Open(ctx context.Context, sha string) (port.Artifact, io.ReadCloser, error) {
	var a port.Artifact
	if !digestPattern.MatchString(sha) {
		return a, nil, contractError(pc.InvalidContract, "invalid artifact digest")
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return a, nil, err
	}
	for _, e := range entries {
		if err = ctx.Err(); err != nil {
			return a, nil, err
		}
		if !e.IsDir() || !pluginIDPattern.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(s.root, e.Name(), sha)
		if _, err = os.Lstat(path); os.IsNotExist(err) {
			continue
		}
		a, b, err := s.read(path)
		if err != nil {
			return a, nil, err
		}
		return a, io.NopCloser(bytes.NewReader(b)), nil
	}
	return a, nil, contractError(pc.Unavailable, "artifact missing")
}

var _ port.PackageStore = (*FilePackages)(nil)

// Prune retains five rollback versions plus every live/desired protected digest.
// Caller serializes with lifecycle and skips this while old leases are draining.
func (s *FilePackages) Prune(ctx context.Context, protected map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !pluginIDPattern.MatchString(entry.Name()) {
			continue
		}
		parent := filepath.Join(s.root, entry.Name())
		versions, err := os.ReadDir(parent)
		if err != nil {
			return err
		}
		type version struct {
			name     string
			modified time.Time
		}
		var candidates []version
		for _, v := range versions {
			if err = ctx.Err(); err != nil {
				return err
			}
			if !v.IsDir() {
				continue
			}
			info, e := v.Info()
			if e != nil {
				return e
			}
			path := filepath.Join(parent, v.Name())
			if strings.HasPrefix(v.Name(), ".staging-") {
				if time.Since(info.ModTime()) > 24*time.Hour {
					if e = os.RemoveAll(path); e != nil {
						return e
					}
				}
				continue
			}
			if digestPattern.MatchString(v.Name()) {
				candidates = append(candidates, version{v.Name(), info.ModTime()})
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].modified.After(candidates[j].modified) })
		for i, v := range candidates {
			if i < 5 || protected[v.name] {
				continue
			}
			if err = os.RemoveAll(filepath.Join(parent, v.name)); err != nil {
				return err
			}
		}
	}
	return nil
}
