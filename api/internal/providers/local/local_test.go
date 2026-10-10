package local

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers"
)

func TestFS_BlocksPathTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	fs, err := NewFS(filepath.Join(root, "fs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, bad := range []string{"../x", "a/../../x", "/etc/passwd", "a/../../../etc"} {
		if err := fs.WriteFile(ctx, bad, []byte("x")); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
	// Symlink inside the root pointing outside must not be followable.
	if err := os.Symlink(outside, filepath.Join(root, "fs", "link")); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFile(ctx, "link/pwned.txt", []byte("x")); err == nil {
		t.Fatal("expected a symlink escaping the root to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Fatal("file was written outside the provider root")
	}
	if err := fs.Remove(ctx, "."); err == nil {
		t.Fatal("removing the root must be refused")
	}
}

func TestFS_RoundTrip(t *testing.T) {
	fs, _ := NewFS(t.TempDir())
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "site/wp-config.php", []byte("<?php")); err != nil {
		t.Fatal(err)
	}
	b, err := fs.ReadFile(ctx, "site/wp-config.php")
	if err != nil || string(b) != "<?php" {
		t.Fatalf("read: %v %q", err, b)
	}
	if n, _ := fs.DiskUsage(ctx, "site"); n != 5 {
		t.Fatalf("disk usage %d", n)
	}
	if _, err := fs.ReadFile(ctx, "missing"); err != providers.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestBackups_CreateVerifyRestoreRoundTripAndCorruption(t *testing.T) {
	root := t.TempDir()
	src, _ := NewFS(filepath.Join(root, "src"))
	ctx := context.Background()
	_ = src.WriteFile(ctx, "site/index.php", []byte("hello"))
	_ = src.WriteFile(ctx, "site/uploads/a.jpg", []byte("img"))
	bk, err := NewBackups(filepath.Join(root, "bk"), src)
	if err != nil {
		t.Fatal(err)
	}
	art, err := bk.Create(ctx, "b1", providers.BackupSource{Paths: []string{"site"}, DatabaseDump: func(_ context.Context, w io.Writer) error {
		_, err := w.Write([]byte("CREATE TABLE t;"))
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := bk.Verify(ctx, art.Ref, art.SHA256); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := bk.Verify(ctx, art.Ref, strings.Repeat("0", 64)); err == nil {
		t.Fatal("expected checksum mismatch")
	}

	dest, _ := NewFS(filepath.Join(root, "dest"))
	var db bytes.Buffer
	if err := bk.Restore(ctx, art.Ref, dest, func(_ context.Context, r io.Reader) error { _, e := io.Copy(&db, r); return e }); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if b, _ := dest.ReadFile(ctx, "site/index.php"); string(b) != "hello" {
		t.Fatalf("restored content %q", b)
	}
	if db.String() != "CREATE TABLE t;" {
		t.Fatalf("db dump %q", db.String())
	}

	// Corrupt the archive on disk: verification must fail.
	p, _ := bk.refPath(art.Ref)
	data, _ := os.ReadFile(p)
	data[len(data)/2] ^= 0xff
	_ = os.WriteFile(p, data, 0o640)
	if err := bk.Verify(ctx, art.Ref, art.SHA256); err == nil {
		t.Fatal("expected verification of a corrupted archive to fail")
	}
	if err := bk.Verify(ctx, "local://../../etc/passwd", "x"); err == nil {
		t.Fatal("expected a traversal ref to be rejected")
	}
}

func TestCerts_IssuesRealX509AndInspects(t *testing.T) {
	c, err := NewCerts()
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Issue(context.Background(), providers.IssueRequest{Domains: []string{"a.example.test", "www.a.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b.KeyPEM, []byte("PRIVATE KEY")) {
		t.Fatal("expected a private key")
	}
	info, err := c.Inspect(context.Background(), b.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	if info.Subject != "a.example.test" || len(info.DNSNames) != 2 || !info.NotAfter.After(info.NotBefore) {
		t.Fatalf("unexpected cert info %+v", info)
	}
	if _, err := c.Issue(context.Background(), providers.IssueRequest{}); err == nil {
		t.Fatal("expected an error for no domains")
	}
}

func TestDNS_UpsertIsIdempotentAndValidates(t *testing.T) {
	d := NewDNS()
	ctx := context.Background()
	_ = d.EnsureZone(ctx, "x.test")
	r1, err := d.UpsertRecord(ctx, "x.test", providers.DNSRecord{Type: "A", Name: "@", Value: "203.0.113.5", TTL: 300})
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := d.UpsertRecord(ctx, "x.test", providers.DNSRecord{Type: "A", Name: "@", Value: "203.0.113.5", TTL: 600})
	if r1.ID != r2.ID {
		t.Fatal("upsert must reuse the record id")
	}
	if recs, _ := d.ListRecords(ctx, "x.test"); len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if _, err := d.UpsertRecord(ctx, "x.test", providers.DNSRecord{Type: "A", Name: "@", Value: "not-an-ip"}); err == nil {
		t.Fatal("expected invalid A value to be rejected")
	}
	if ok, _ := d.CheckPropagation(ctx, "x.test", r1); !ok {
		t.Fatal("expected propagation")
	}
}

func TestMonitoring_SSRFPolicyAndRealProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	ctx := context.Background()

	strict := NewMonitoring(netpolicy.Policy{Level: netpolicy.PublicOnly})
	res, err := strict.CheckHTTP(ctx, srv.URL, 200)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("PublicOnly policy must refuse to probe a loopback target")
	}

	open := NewMonitoring(netpolicy.Policy{Level: netpolicy.InternalAllowed})
	res, _ = open.CheckHTTP(ctx, srv.URL, 200)
	if !res.OK {
		t.Fatalf("expected healthy probe, got %+v", res)
	}
	res, _ = open.CheckHTTP(ctx, srv.URL, 404)
	if res.OK {
		t.Fatal("expected a status mismatch to fail")
	}
	if _, err := open.CheckHTTP(ctx, "file:///etc/passwd", 200); err == nil {
		t.Fatal("non-http schemes must be rejected")
	}
	if m, _ := open.CollectNodeMetrics(ctx, "x"); m.DiskTotalGB <= 0 {
		t.Fatal("expected real disk metrics")
	}
}
