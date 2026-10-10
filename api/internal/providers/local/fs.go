// Package local contains real, self-contained provider implementations that
// work on a developer machine without any cloud account: a rooted filesystem,
// tar.gz backups with SHA-256 verification, a local certificate authority,
// a file-backed DNS zone store, and real HTTP/TCP/DNS/TLS probes.
package local

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nodera/nodera/internal/providers"
)

// SafeJoin resolves rel inside root and rejects anything that would escape it
// (absolute paths, .. segments, or symlinks that point outside root).
func SafeJoin(root, rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative to the provider root", rel)
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			return "", fmt.Errorf("path %q escapes the provider root", rel)
		}
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(rootAbs, rel)
	// Resolve symlinks on the deepest existing ancestor and re-check containment.
	probe := full
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		next := filepath.Dir(probe)
		if next == probe {
			break
		}
		probe = next
	}
	resolved, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", err
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", err
	}
	if resolved != rootResolved && !strings.HasPrefix(resolved, rootResolved+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q resolves outside the provider root", rel)
	}
	return full, nil
}

// FS is a FilesystemProvider rooted at a directory.
type FS struct{ root string }

func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &FS{root: root}, nil
}

func (f *FS) AbsPath(p string) (string, error) { return SafeJoin(f.root, p) }
func (f *FS) MkdirAll(_ context.Context, p string) error {
	full, err := SafeJoin(f.root, p)
	if err != nil {
		return err
	}
	return os.MkdirAll(full, 0o750)
}
func (f *FS) WriteFile(_ context.Context, p string, data []byte) error {
	full, err := SafeJoin(f.root, p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o640)
}
func (f *FS) ReadFile(_ context.Context, p string) ([]byte, error) {
	full, err := SafeJoin(f.root, p)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if os.IsNotExist(err) {
		return nil, providers.ErrNotFound
	}
	return b, err
}
func (f *FS) Exists(_ context.Context, p string) (bool, error) {
	full, err := SafeJoin(f.root, p)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(full)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
func (f *FS) Remove(_ context.Context, p string) error {
	full, err := SafeJoin(f.root, p)
	if err != nil {
		return err
	}
	rootAbs, _ := filepath.Abs(f.root)
	if full == rootAbs {
		return fmt.Errorf("refusing to remove the provider root")
	}
	return os.RemoveAll(full)
}
func (f *FS) List(_ context.Context, p string) ([]providers.FileInfo, error) {
	full, err := SafeJoin(f.root, p)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full)
	if os.IsNotExist(err) {
		return nil, providers.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var out []providers.FileInfo
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, providers.FileInfo{Path: filepath.ToSlash(filepath.Join(p, e.Name())), Size: info.Size(), IsDir: e.IsDir()})
	}
	return out, nil
}
func (f *FS) DiskUsage(_ context.Context, p string) (int64, error) {
	full, err := SafeJoin(f.root, p)
	if err != nil {
		return 0, err
	}
	var n int64
	err = filepath.WalkDir(full, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n, err
}
