package offsite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/local"
)

const refPrefix = "sftp://"

// Backups is a providers.BackupProvider that stores archives off-site. The
// local streaming provider builds each archive; this layer uploads it, checks
// the remote size, and keeps a SHA-256 sidecar next to it so a restore can
// verify what it downloaded before touching anything. Refs created before the
// destination was switched ("local://") keep working through the local provider.
type Backups struct {
	local     *local.Backups
	store     Store
	keepLocal bool
}

func NewBackups(l *local.Backups, store Store, keepLocal bool) *Backups {
	return &Backups{local: l, store: store, keepLocal: keepLocal}
}

func objectName(ref string) (string, bool) {
	if !strings.HasPrefix(ref, refPrefix) {
		return "", false
	}
	return strings.TrimPrefix(ref, refPrefix), true
}

func (b *Backups) Create(ctx context.Context, id string, src providers.BackupSource) (providers.BackupArtifact, error) {
	art, err := b.local.Create(ctx, id, src)
	if err != nil {
		return providers.BackupArtifact{}, err
	}
	localPath, err := b.local.Path(art.Ref)
	if err != nil {
		return providers.BackupArtifact{}, err
	}
	name := id + ".tar.gz"
	cleanupRemote := func() {
		// Best effort, on a fresh context: the failure may be the cancellation itself.
		c := context.WithoutCancel(ctx)
		_ = b.store.Delete(c, name)
		_ = b.store.Delete(c, name+".sha256")
	}
	fail := func(err error) (providers.BackupArtifact, error) {
		_ = b.local.Delete(ctx, art.Ref)
		return providers.BackupArtifact{}, err
	}

	f, err := os.Open(localPath)
	if err != nil {
		return fail(err)
	}
	n, err := b.store.Put(ctx, name, f)
	f.Close()
	if err != nil {
		cleanupRemote()
		return fail(fmt.Errorf("off-site upload failed: %w", err))
	}
	if n != art.Size {
		cleanupRemote()
		return fail(fmt.Errorf("off-site upload is incomplete: sent %d bytes, archive is %d", n, art.Size))
	}
	if _, err := b.store.Put(ctx, name+".sha256", strings.NewReader(art.SHA256+"\n")); err != nil {
		cleanupRemote()
		return fail(fmt.Errorf("off-site checksum upload failed: %w", err))
	}
	if !b.keepLocal {
		_ = b.local.Delete(ctx, art.Ref)
	}
	return providers.BackupArtifact{Ref: refPrefix + name, Size: art.Size, SHA256: art.SHA256}, nil
}

// hashRemote streams the remote object through SHA-256.
func (b *Backups) hashRemote(ctx context.Context, name string) (string, error) {
	rc, err := b.store.Get(ctx, name)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Verify re-reads the REMOTE copy and compares its SHA-256, so it proves what
// is actually stored off-site, not what was sent.
func (b *Backups) Verify(ctx context.Context, ref, want string) error {
	name, ok := objectName(ref)
	if !ok {
		return b.local.Verify(ctx, ref, want)
	}
	got, err := b.hashRemote(ctx, name)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("checksum mismatch")
	}
	return nil
}

func (b *Backups) Restore(ctx context.Context, ref string, dest providers.FilesystemProvider, dbLoad func(context.Context, io.Reader) error) error {
	name, ok := objectName(ref)
	if !ok {
		return b.local.Restore(ctx, ref, dest, dbLoad)
	}
	want, err := b.sidecar(ctx, name)
	if err != nil {
		return err
	}
	// Download to a temporary file while hashing, and only extract once the
	// hash matches: a corrupt or tampered remote object never reaches the
	// destination filesystem.
	tmp, err := b.local.TempFile()
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	rc, err := b.store.Get(ctx, name)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return errors.New("refusing to restore: downloaded archive does not match its recorded checksum")
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return local.ExtractArchive(ctx, tmp, dest, dbLoad)
}

func (b *Backups) sidecar(ctx context.Context, name string) (string, error) {
	rc, err := b.store.Get(ctx, name+".sha256")
	if err != nil {
		return "", fmt.Errorf("checksum file: %w", err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 256))
	if err != nil {
		return "", err
	}
	sum := strings.TrimSpace(string(raw))
	if len(sum) != 64 {
		return "", errors.New("checksum file is malformed")
	}
	return sum, nil
}

func (b *Backups) Delete(ctx context.Context, ref string) error {
	name, ok := objectName(ref)
	if !ok {
		return b.local.Delete(ctx, ref)
	}
	if err := b.store.Delete(ctx, name); err != nil {
		return err
	}
	if err := b.store.Delete(ctx, name+".sha256"); err != nil {
		return err
	}
	_ = b.local.Delete(ctx, "local://"+name) // the optional staging copy
	return nil
}

var _ providers.BackupProvider = (*Backups)(nil)
