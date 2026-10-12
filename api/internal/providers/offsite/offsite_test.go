package offsite

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/local"
)

const testPassword = "s3cret-test-password-do-not-leak"

// testServer is a real SSH+SFTP server on loopback serving a temp directory, so
// the host-key, authentication and transfer paths run for real.
type testServer struct {
	addr        string
	dir         string
	fingerprint string
	clientKey   []byte // PEM private key accepted by the server
}

func startServer(t *testing.T) *testServer {
	t.Helper()
	dir := t.TempDir()
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPriv)

	cliPub, cliPriv, _ := ed25519.GenerateKey(rand.Reader)
	blk, err := ssh.MarshalPrivateKey(cliPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	cliSSHPub, _ := ssh.NewPublicKey(cliPub)

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == "box" && string(p) == testPassword {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == "box" && string(k.Marshal()) == string(cliSSHPub.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					conn.Close()
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "session" {
						_ = nc.Reject(ssh.UnknownChannelType, "no")
						continue
					}
					ch, creqs, _ := nc.Accept()
					go func() {
						for r := range creqs {
							ok := r.Type == "subsystem" && len(r.Payload) >= 4 && string(r.Payload[4:]) == "sftp"
							_ = r.Reply(ok, nil)
							if ok {
								srv, err := sftp.NewServer(ch)
								if err == nil {
									_ = srv.Serve()
									srv.Close()
								}
								ch.Close()
							}
						}
					}()
				}
			}()
		}
	}()
	return &testServer{addr: ln.Addr().String(), dir: dir, fingerprint: ssh.FingerprintSHA256(hostSigner.PublicKey()), clientKey: pem.EncodeToMemory(blk)}
}

func (s *testServer) cfg() SFTPConfig {
	return SFTPConfig{Addr: s.addr, User: "box", Dir: s.dir, Password: testPassword, HostKeySHA256: []string{s.fingerprint}, DialTimeout: 5 * time.Second}
}

type rig struct {
	srv  *testServer
	bk   *Backups
	loc  *local.Backups
	src  *local.FS
	root string
}

func newRig(t *testing.T, mutate func(*SFTPConfig)) *rig {
	t.Helper()
	srv := startServer(t)
	cfg := srv.cfg()
	if mutate != nil {
		mutate(&cfg)
	}
	store, err := NewSFTPStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	src, _ := local.NewFS(filepath.Join(root, "src"))
	loc, _ := local.NewBackups(filepath.Join(root, "staging"), src)
	return &rig{srv: srv, bk: NewBackups(loc, store, false), loc: loc, src: src, root: root}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, _ := os.ReadDir(dir)
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestRoundTripUploadVerifyRestoreDelete(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	_ = r.src.WriteFile(ctx, "site/index.php", []byte("hello"))
	art, err := r.bk.Create(ctx, "b1", providers.BackupSource{Paths: []string{"site"}, DatabaseDump: func(_ context.Context, w io.Writer) error {
		_, err := w.Write([]byte("CREATE TABLE t;"))
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	if art.Ref != "sftp://b1.tar.gz" {
		t.Fatalf("ref %q", art.Ref)
	}
	if got := entries(t, r.srv.dir); len(got) != 2 {
		t.Fatalf("remote should hold the archive and its checksum, got %v", got)
	}
	st, _ := os.Stat(filepath.Join(r.srv.dir, "b1.tar.gz"))
	if st.Size() != art.Size {
		t.Fatalf("remote size %d != %d", st.Size(), art.Size)
	}
	if got := entries(t, filepath.Join(r.root, "staging")); len(got) != 0 {
		t.Fatalf("staging copy must be removed after a verified upload, got %v", got)
	}
	if err := r.bk.Verify(ctx, art.Ref, art.SHA256); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := r.bk.Verify(ctx, art.Ref, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong checksum must fail")
	}

	dest, _ := local.NewFS(filepath.Join(r.root, "dest"))
	var db strings.Builder
	if err := r.bk.Restore(ctx, art.Ref, dest, func(_ context.Context, rd io.Reader) error { _, e := io.Copy(&db, rd); return e }); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if b, _ := dest.ReadFile(ctx, "site/index.php"); string(b) != "hello" || db.String() != "CREATE TABLE t;" {
		t.Fatalf("restored %q / %q", b, db.String())
	}
	if got := entries(t, filepath.Join(r.root, "staging")); len(got) != 0 {
		t.Fatalf("download temp file must be cleaned up, got %v", got)
	}

	if err := r.bk.Delete(ctx, art.Ref); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, r.srv.dir); len(got) != 0 {
		t.Fatalf("delete must remove the archive and checksum, got %v", got)
	}
	if err := r.bk.Verify(ctx, art.Ref, art.SHA256); !errors.Is(err, providers.ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

func TestCorruptedRemoteArchiveIsDetectedAndNeverRestored(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	_ = r.src.WriteFile(ctx, "site/index.php", []byte("hello"))
	art, err := r.bk.Create(ctx, "b1", providers.BackupSource{Paths: []string{"site"}})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(r.srv.dir, "b1.tar.gz")
	data, _ := os.ReadFile(p)
	data[len(data)/2] ^= 0xff
	_ = os.WriteFile(p, data, 0o600)

	if err := r.bk.Verify(ctx, art.Ref, art.SHA256); err == nil {
		t.Fatal("corruption must be detected by Verify")
	}
	dest, _ := local.NewFS(filepath.Join(r.root, "dest"))
	if err := r.bk.Restore(ctx, art.Ref, dest, nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("restore must refuse a corrupted archive, got %v", err)
	}
	if ok, _ := dest.Exists(ctx, "site/index.php"); ok {
		t.Fatal("nothing may be written to the destination from a corrupted archive")
	}
}

func TestHostKeyMustMatchPinnedFingerprint(t *testing.T) {
	r := newRig(t, func(c *SFTPConfig) { c.HostKeySHA256 = []string{"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} })
	ctx := context.Background()
	_ = r.src.WriteFile(ctx, "site/a", []byte("x"))
	_, err := r.bk.Create(ctx, "b1", providers.BackupSource{Paths: []string{"site"}})
	if !errors.Is(err, ErrHostKey) {
		t.Fatalf("want host key rejection, got %v", err)
	}
	if got := entries(t, r.srv.dir); len(got) != 0 {
		t.Fatalf("nothing may be uploaded to an unverified host: %v", got)
	}
	if got := entries(t, filepath.Join(r.root, "staging")); len(got) != 0 {
		t.Fatalf("a failed upload must not leave an apparently-valid local backup: %v", got)
	}
	if _, err := NewSFTPStore(SFTPConfig{Addr: "h:1", User: "u", Dir: "/d", Password: "p"}); err == nil {
		t.Fatal("a store without a pinned fingerprint must be refused")
	}
}

func TestAuthenticationFailureDoesNotLeakCredentials(t *testing.T) {
	r := newRig(t, func(c *SFTPConfig) { c.Password = "wrong-password-12345" })
	ctx := context.Background()
	_ = r.src.WriteFile(ctx, "site/a", []byte("x"))
	_, err := r.bk.Create(ctx, "b1", providers.BackupSource{Paths: []string{"site"}})
	if err == nil {
		t.Fatal("bad credentials must fail")
	}
	for _, secret := range []string{"wrong-password-12345", testPassword} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks a credential: %v", err)
		}
	}
}

func TestPrivateKeyAuthentication(t *testing.T) {
	srv := startServer(t)
	cfg := srv.cfg()
	cfg.Password, cfg.PrivateKeyPEM = "", srv.clientKey
	store, err := NewSFTPStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), "k.bin", strings.NewReader("data")); err != nil {
		t.Fatalf("key auth: %v", err)
	}
	if n, err := store.Size(context.Background(), "k.bin"); err != nil || n != 4 {
		t.Fatalf("size %d %v", n, err)
	}
}

func TestFailedUploadLeavesNoBackupAnywhere(t *testing.T) {
	r := newRig(t, func(c *SFTPConfig) { c.Dir = filepath.Join(c.Dir, "does-not-exist") })
	ctx := context.Background()
	_ = r.src.WriteFile(ctx, "site/a", []byte("x"))
	_, err := r.bk.Create(ctx, "b1", providers.BackupSource{Paths: []string{"site"}})
	if err == nil {
		t.Fatal("upload to a missing directory must fail")
	}
	if got := entries(t, filepath.Join(r.root, "staging")); len(got) != 0 {
		t.Fatalf("staging must be clean after a failed upload: %v", got)
	}
}

func TestCancelledUploadStops(t *testing.T) {
	r := newRig(t, nil)
	_ = r.src.WriteFile(context.Background(), "site/a", []byte("x"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.bk.Create(ctx, "b1", providers.BackupSource{Paths: []string{"site"}}); err == nil {
		t.Fatal("a cancelled run must fail")
	}
	if got := entries(t, r.srv.dir); len(got) != 0 {
		t.Fatalf("remote must be clean: %v", got)
	}
}

func TestObjectNamesAreValidated(t *testing.T) {
	srv := startServer(t)
	store, _ := NewSFTPStore(srv.cfg())
	for _, bad := range []string{"../x", "a/b", ".hidden", "", "x y"} {
		if _, err := store.Size(context.Background(), bad); err == nil || errors.Is(err, providers.ErrNotFound) {
			t.Errorf("name %q must be rejected before any remote call, got %v", bad, err)
		}
	}
}

func TestLocalRefsKeepWorkingAfterSwitchingDestination(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	_ = r.src.WriteFile(ctx, "site/a", []byte("x"))
	art, err := r.loc.Create(ctx, "old", providers.BackupSource{Paths: []string{"site"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.bk.Verify(ctx, art.Ref, art.SHA256); err != nil {
		t.Fatalf("a pre-existing local:// backup must still verify: %v", err)
	}
}

// A pin bound to the server's key type works and is what lets an operator pin
// one key instead of every key the server offers.
func TestHostKeyPinBoundToKeyType(t *testing.T) {
	srv := startServer(t)
	cfg := srv.cfg()
	cfg.HostKeySHA256 = []string{"ssh-ed25519 " + srv.fingerprint}
	store, err := NewSFTPStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), "a.bin", strings.NewReader("x")); err != nil {
		t.Fatalf("bound pin should connect: %v", err)
	}
	cfg.HostKeySHA256 = []string{"ssh-rsa " + srv.fingerprint} // server has no RSA key: must not connect
	store, _ = NewSFTPStore(cfg)
	if _, err := store.Put(context.Background(), "b.bin", strings.NewReader("x")); err == nil {
		t.Fatal("a pin bound to a key type the server does not offer must fail")
	}
	for _, bad := range []string{"", "ssh-ed25519", "md5:aa:bb", "a b c"} {
		cfg.HostKeySHA256 = []string{bad}
		if _, err := NewSFTPStore(cfg); err == nil {
			t.Errorf("pin %q must be rejected", bad)
		}
	}
}
