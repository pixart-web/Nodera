package local

import (
	"archive/tar"
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

// maxRestoreEntry bounds a single extracted file (decompression-bomb guard).
const maxRestoreEntry = 64 << 30

// Backups writes tar.gz archives (optionally including a database dump as
// "database.sql") into a directory and verifies them by SHA-256.
//
// Everything is streamed: the source tree is walked and written straight into
// gzip→tar→file with the checksum computed incrementally, database dumps are
// staged in a temporary file (a tar header needs the size up front) and memory
// stays bounded by copy buffers whatever the size of the site. Temporary files
// are removed on success, failure and cancellation, and the archive only
// appears under its final name (atomic rename) once it is complete and synced,
// so a failed run never leaves something that looks like a valid backup.
type Backups struct {
	dir string
	src *FS // source filesystem the Paths are relative to
}

func NewBackups(dir string, src *FS) (*Backups, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	b := &Backups{dir: dir, src: src}
	b.sweepStaleTemp()
	return b, nil
}

// sweepStaleTemp removes temp files left behind by a crash (kill -9, power
// loss) in a previous run. Called at start-up only.
func (b *Backups) sweepStaleTemp() {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			_ = os.Remove(filepath.Join(b.dir, e.Name()))
		}
	}
}

const tmpPrefix = ".tmp-backup-"

func (b *Backups) refPath(ref string) (string, error) {
	name := strings.TrimPrefix(ref, "local://")
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid backup ref %q", ref)
	}
	return filepath.Join(b.dir, name), nil
}

// TempFile returns a private temporary file inside the backup directory. Files
// named this way are swept at the next start-up if a crash leaves them behind.
func (b *Backups) TempFile() (*os.File, error) {
	return os.CreateTemp(b.dir, tmpPrefix+"dl-*")
}

// Path returns the on-disk path of an archive (used by off-site wrappers).
func (b *Backups) Path(ref string) (string, error) { return b.refPath(ref) }

func (b *Backups) Create(ctx context.Context, id string, src providers.BackupSource) (art providers.BackupArtifact, err error) {
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return providers.BackupArtifact{}, fmt.Errorf("invalid backup id")
	}
	base, err := b.src.AbsPath(".")
	if err != nil {
		return providers.BackupArtifact{}, err
	}
	out, err := os.CreateTemp(b.dir, tmpPrefix+"*")
	if err != nil {
		return providers.BackupArtifact{}, err
	}
	outName := out.Name()
	var dumpName string
	defer func() { // runs on success too: after the rename outName no longer exists
		_ = out.Close()
		_ = os.Remove(outName)
		if dumpName != "" {
			_ = os.Remove(dumpName)
		}
	}()

	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(out, h)}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)

	for _, p := range src.Paths {
		full, err := b.src.AbsPath(p)
		if err != nil {
			return providers.BackupArtifact{}, err
		}
		err = filepath.Walk(full, func(path string, info os.FileInfo, werr error) error {
			if werr != nil {
				return werr // an unreadable file must fail the backup, not silently shrink it
			}
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if !info.Mode().IsRegular() {
				return nil // directories are implied; symlinks/devices are never archived
			}
			rel, _ := filepath.Rel(base, path) // keep the path relative to the FS root so Restore puts files back where they were
			return addFile(ctx, tw, "files/"+filepath.ToSlash(rel), path, info.Size())
		})
		if err != nil {
			return providers.BackupArtifact{}, err
		}
	}
	if src.DatabaseDump != nil {
		dump, err := os.CreateTemp(b.dir, tmpPrefix+"db-*")
		if err != nil {
			return providers.BackupArtifact{}, err
		}
		dumpName = dump.Name()
		derr := src.DatabaseDump(ctx, dump)
		if cerr := dump.Close(); derr == nil {
			derr = cerr
		}
		if derr != nil {
			return providers.BackupArtifact{}, fmt.Errorf("database dump: %w", derr)
		}
		st, err := os.Stat(dumpName)
		if err != nil {
			return providers.BackupArtifact{}, err
		}
		if err := addFile(ctx, tw, "database.sql", dumpName, st.Size()); err != nil {
			return providers.BackupArtifact{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return providers.BackupArtifact{}, err
	}
	if err := gz.Close(); err != nil {
		return providers.BackupArtifact{}, err
	}
	if err := out.Sync(); err != nil {
		return providers.BackupArtifact{}, err
	}
	if err := out.Close(); err != nil {
		return providers.BackupArtifact{}, err
	}
	ref := "local://" + id + ".tar.gz"
	path, err := b.refPath(ref)
	if err != nil {
		return providers.BackupArtifact{}, err
	}
	if err := os.Chmod(outName, 0o640); err != nil {
		return providers.BackupArtifact{}, err
	}
	if err := os.Rename(outName, path); err != nil {
		return providers.BackupArtifact{}, err
	}
	return providers.BackupArtifact{Ref: ref, Size: cw.n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// addFile appends one regular file, streaming its content. A file that changes
// size while being read fails the backup instead of producing a corrupt entry.
func addFile(ctx context.Context, tw *tar.Writer, name, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o640, Size: size, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	n, err := io.Copy(tw, io.LimitReader(&ctxReader{ctx: ctx, r: f}, size))
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("%s changed while being backed up", name)
	}
	return nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// Verify streams the archive through SHA-256; nothing is loaded into memory.
func (b *Backups) Verify(ctx context.Context, ref, want string) error {
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
	h := sha256.New()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: f}); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
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
	return ExtractArchive(ctx, f, dest, dbLoad)
}

// ExtractArchive unpacks a backup archive stream into dest, streaming each
// file. Entries other than regular files are ignored, and dest enforces root
// containment so an entry such as ../../etc/x is rejected (path traversal guard).
func ExtractArchive(ctx context.Context, r io.Reader, dest providers.FilesystemProvider, dbLoad func(context.Context, io.Reader) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	sw, canStream := dest.(providers.StreamWriter)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		entry := io.LimitReader(tr, maxRestoreEntry)
		if hdr.Name == "database.sql" {
			if dbLoad != nil {
				if err := dbLoad(ctx, entry); err != nil {
					return fmt.Errorf("database restore: %w", err)
				}
			}
			continue
		}
		name := strings.TrimPrefix(hdr.Name, "files/")
		if canStream {
			err = sw.WriteStream(ctx, name, entry)
		} else {
			var data []byte
			if data, err = io.ReadAll(io.LimitReader(entry, 1<<30)); err == nil {
				err = dest.WriteFile(ctx, name, data)
			}
		}
		if err != nil {
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
