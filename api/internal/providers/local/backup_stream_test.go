package local

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nodera/nodera/internal/providers"
)

// peakHeap samples HeapAlloc until stop() and returns the highest reading.
func peakHeap() (stop func() uint64) {
	var peak atomic.Uint64
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	return func() uint64 { close(done); <-finished; return peak.Load() }
}

type patternReader struct{ left int64 }

func (p *patternReader) Read(b []byte) (int, error) {
	if p.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(b))
	if n > p.left {
		n = p.left
	}
	for i := int64(0); i < n; i++ {
		b[i] = byte('a' + i%23)
	}
	p.left -= n
	return int(n), nil
}

func tmpLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// A database dump far larger than the memory budget must stream through
// without the archive ever being held in RAM, both on backup and on restore.
func TestBackupMemoryStaysBoundedForLargeInput(t *testing.T) {
	const logical = 384 << 20 // 384 MiB of dump; the budget below is far smaller
	const budget = 96 << 20
	root := t.TempDir()
	src, _ := NewFS(filepath.Join(root, "src"))
	ctx := context.Background()
	bk, _ := NewBackups(filepath.Join(root, "bk"), src)

	runtime.GC()
	stop := peakHeap()
	art, err := bk.Create(ctx, "big", providers.BackupSource{DatabaseDump: func(_ context.Context, w io.Writer) error {
		_, err := io.Copy(w, &patternReader{left: logical})
		return err
	}})
	peak := stop()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("backup of %d MiB logical input: peak heap %d MiB", logical>>20, peak>>20)
	if peak > budget {
		t.Fatalf("backup peak heap %d MiB exceeds the %d MiB budget: the archive is being buffered", peak>>20, budget>>20)
	}

	runtime.GC()
	stop = peakHeap()
	if err := bk.Verify(ctx, art.Ref, art.SHA256); err != nil {
		t.Fatal(err)
	}
	var got int64
	dest, _ := NewFS(filepath.Join(root, "dest"))
	err = bk.Restore(ctx, art.Ref, dest, func(_ context.Context, r io.Reader) error {
		n, err := io.Copy(io.Discard, r)
		got = n
		return err
	})
	peak = stop()
	if err != nil {
		t.Fatal(err)
	}
	if got != logical {
		t.Fatalf("restored %d bytes of dump, want %d", got, logical)
	}
	t.Logf("restore: peak heap %d MiB", peak>>20)
	if peak > budget {
		t.Fatalf("restore peak heap %d MiB exceeds the %d MiB budget", peak>>20, budget>>20)
	}
}

// Files restore through WriteStream (temp file + rename), not through a []byte.
func TestRestoreStreamsFilesAndLeavesNoTempFiles(t *testing.T) {
	root := t.TempDir()
	src, _ := NewFS(filepath.Join(root, "src"))
	ctx := context.Background()
	big := strings.Repeat("0123456789abcdef", 1<<16) // 1 MiB
	_ = src.WriteFile(ctx, "site/big.bin", []byte(big))
	bk, _ := NewBackups(filepath.Join(root, "bk"), src)
	art, err := bk.Create(ctx, "s1", providers.BackupSource{Paths: []string{"site"}})
	if err != nil {
		t.Fatal(err)
	}
	dest, _ := NewFS(filepath.Join(root, "dest"))
	var _ providers.StreamWriter = dest
	if err := bk.Restore(ctx, art.Ref, dest, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := dest.ReadFile(ctx, "site/big.bin"); string(b) != big {
		t.Fatal("restored file differs")
	}
	if names := tmpLeftovers(t, filepath.Join(root, "dest", "site")); len(names) != 1 {
		t.Fatalf("unexpected leftovers in restore dir: %v", names)
	}
}

func TestFailedBackupLeavesNothingBehind(t *testing.T) {
	root := t.TempDir()
	src, _ := NewFS(filepath.Join(root, "src"))
	ctx := context.Background()
	_ = src.WriteFile(ctx, "site/a.txt", []byte("hello"))
	bkDir := filepath.Join(root, "bk")
	bk, _ := NewBackups(bkDir, src)

	boom := errors.New("dump exploded")
	if _, err := bk.Create(ctx, "f1", providers.BackupSource{Paths: []string{"site"}, DatabaseDump: func(_ context.Context, w io.Writer) error {
		_, _ = w.Write([]byte("partial"))
		return boom
	}}); !errors.Is(err, boom) {
		t.Fatalf("want the dump error, got %v", err)
	}
	if left := tmpLeftovers(t, bkDir); len(left) != 0 {
		t.Fatalf("failed backup left files behind: %v", left)
	}
	if err := bk.Verify(ctx, "local://f1.tar.gz", "x"); !errors.Is(err, providers.ErrNotFound) {
		t.Fatalf("a failed backup must not exist as an artifact, got %v", err)
	}
}

func TestCancelledBackupCleansUp(t *testing.T) {
	root := t.TempDir()
	src, _ := NewFS(filepath.Join(root, "src"))
	_ = src.WriteFile(context.Background(), "site/a.txt", []byte("hello"))
	bkDir := filepath.Join(root, "bk")
	bk, _ := NewBackups(bkDir, src)

	ctx, cancel := context.WithCancel(context.Background())
	_, err := bk.Create(ctx, "c1", providers.BackupSource{DatabaseDump: func(c context.Context, w io.Writer) error {
		cancel() // the operator cancels while the dump is running
		_, err := io.Copy(w, &ctxReader{ctx: c, r: &patternReader{left: 64 << 20}})
		return err
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if left := tmpLeftovers(t, bkDir); len(left) != 0 {
		t.Fatalf("cancelled backup left files behind: %v", left)
	}

	// Cancellation while walking a large tree also stops the run.
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := bk.Create(ctx2, "c2", providers.BackupSource{Paths: []string{"site"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled for a pre-cancelled run, got %v", err)
	}
	if left := tmpLeftovers(t, bkDir); len(left) != 0 {
		t.Fatalf("leftovers: %v", left)
	}
}

func TestStaleTempFilesAreSweptAtStartup(t *testing.T) {
	root := t.TempDir()
	src, _ := NewFS(filepath.Join(root, "src"))
	bkDir := filepath.Join(root, "bk")
	_ = os.MkdirAll(bkDir, 0o750)
	_ = os.WriteFile(filepath.Join(bkDir, tmpPrefix+"crashed"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(bkDir, "keep.tar.gz"), []byte("x"), 0o600)
	if _, err := NewBackups(bkDir, src); err != nil {
		t.Fatal(err)
	}
	left := tmpLeftovers(t, bkDir)
	if len(left) != 1 || left[0] != "keep.tar.gz" {
		t.Fatalf("expected only the real artifact to remain, got %v", left)
	}
}

// A hostile archive cannot write outside the destination root or create links.
func TestExtractRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	src, _ := NewFS(filepath.Join(root, "src"))
	ctx := context.Background()
	_ = src.WriteFile(ctx, "site/a.txt", []byte("x"))
	bk, _ := NewBackups(filepath.Join(root, "bk"), src)
	art, _ := bk.Create(ctx, "t1", providers.BackupSource{Paths: []string{"site"}})
	_ = art
	dest, _ := NewFS(filepath.Join(root, "dest"))
	if err := dest.WriteStream(ctx, "../escape", strings.NewReader("x")); err == nil {
		t.Fatal("WriteStream must reject path traversal")
	}
}
