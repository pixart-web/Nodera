package local

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nodera/nodera/internal/providers"
)

// Backups writes tar.gz archives (optionally including a database dump as
// "database.sql") into a directory and verifies them by SHA-256.
type Backups struct {
	dir string
	src *FS // source filesystem the Paths are relative to
}

func NewBackups(dir string, src *FS) (*Backups, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Backups{dir: dir, src: src}, nil
}

func (b *Backups) refPath(ref string) (string, error) {
	name := strings.TrimPrefix(ref, "local://")
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid backup ref %q", ref)
	}
	return filepath.Join(b.dir, name), nil
}

func (b *Backups) Create(ctx context.Context, id string, src providers.BackupSource) (providers.BackupArtifact, error) {
	if id == "" || id != filepath.Base(id) {
		return providers.BackupArtifact{}, fmt.Errorf("invalid backup id")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, p := range src.Paths {
		full, err := b.src.AbsPath(p)
		if err != nil {
			return providers.BackupArtifact{}, err
		}
		root := full
		err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || !info.Mode().IsRegular() {
				return nil // symlinks/devices are never archived
			}
			rel, _ := filepath.Rel(filepath.Dir(root), path)
			hdr := &tar.Header{Name: "files/" + filepath.ToSlash(rel), Mode: 0o640, Size: info.Size()}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		})
		if err != nil {
			return providers.BackupArtifact{}, err
		}
	}
	if src.DatabaseDump != nil {
		var dump bytes.Buffer
		if err := src.DatabaseDump(ctx, &dump); err != nil {
			return providers.BackupArtifact{}, fmt.Errorf("database dump: %w", err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: "database.sql", Mode: 0o640, Size: int64(dump.Len())}); err != nil {
			return providers.BackupArtifact{}, err
		}
		if _, err := tw.Write(dump.Bytes()); err != nil {
			return providers.BackupArtifact{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return providers.BackupArtifact{}, err
	}
	if err := gz.Close(); err != nil {
		return providers.BackupArtifact{}, err
	}
	sum := sha256.Sum256(buf.Bytes())
	ref := "local://" + id + ".tar.gz"
	path, _ := b.refPath(ref)
	if err := os.WriteFile(path, buf.Bytes(), 0o640); err != nil {
		return providers.BackupArtifact{}, err
	}
	return providers.BackupArtifact{Ref: ref, Size: int64(buf.Len()), SHA256: hex.EncodeToString(sum[:])}, nil
}

func (b *Backups) Verify(_ context.Context, ref, want string) error {
	path, err := b.refPath(ref)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return providers.ErrNotFound
	}
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return errors.New("checksum mismatch")
	}
	return nil
}

func (b *Backups) Restore(ctx context.Context, ref string, dest providers.FilesystemProvider, dbLoad func(context.Context, io.Reader) error) error {
	path, err := b.refPath(ref)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return providers.ErrNotFound
	}
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(tr, 1<<30))
		if err != nil {
			return err
		}
		if hdr.Name == "database.sql" {
			if dbLoad != nil {
				if err := dbLoad(ctx, bytes.NewReader(data)); err != nil {
					return fmt.Errorf("database restore: %w", err)
				}
			}
			continue
		}
		// dest.WriteFile enforces root containment, so a malicious archive
		// entry such as ../../etc/x is rejected there (path traversal guard).
		if err := dest.WriteFile(ctx, strings.TrimPrefix(hdr.Name, "files/"), data); err != nil {
			return err
		}
	}
}

func (b *Backups) Delete(_ context.Context, ref string) error {
	path, err := b.refPath(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
