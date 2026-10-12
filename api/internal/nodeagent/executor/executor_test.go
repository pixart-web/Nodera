package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/nodeagent/guard"
	"github.com/nodera/nodera/internal/nodeagent/protocol"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/local"
	"github.com/nodera/nodera/internal/providers/mock"
)

func newExec(t *testing.T) (*Executor, string, *mock.Handles) {
	t.Helper()
	set, h := mock.NewSet()
	ls, err := local.NewSet(t.TempDir(), netpolicy.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	set.FS = ls.FS // real filesystem so database dump/load touch real files
	root, _ := ls.FS.AbsPath(".")
	return &Executor{P: set, Guard: guard.Policy{DataRoot: root, ProtectedNames: []string{"traefik"}}}, root, h
}

func run(t *testing.T, e *Executor, op, params string) (json.RawMessage, error) {
	t.Helper()
	return e.Execute(context.Background(), protocol.Command{Op: op, Params: json.RawMessage(params)})
}

func TestControlPlaneContainersCannotBeTouched(t *testing.T) {
	e, root, _ := newExec(t)
	// Protocol layer: reserved prefix is refused before any provider is reached.
	for _, c := range []struct{ op, p string }{
		{"docker.remove", `{"name":"nodera-cp-api"}`},
		{"docker.stop", `{"name":"nodera-cp-postgres"}`},
		{"docker.create", `{"name":"nodera-cp-api","image":"alpine"}`},
	} {
		if _, err := run(t, e, c.op, c.p); err == nil {
			t.Errorf("%s %s must be refused", c.op, c.p)
		}
	}
	// Executor layer: configured names and labelled containers are protected too.
	if _, err := run(t, e, "docker.remove", `{"name":"traefik"}`); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Errorf("configured protected name must be refused, got %v", err)
	}
	// Docker socket / out-of-root mounts cannot be smuggled in through create.
	bad := `{"name":"evil","image":"alpine","volumes":[{"source":"/var/run/docker.sock","target":"/s"}]}`
	if _, err := run(t, e, "docker.create", bad); err == nil {
		t.Error("docker.sock mount must be refused")
	}
	ok := `{"name":"nodera-blog","image":"alpine","volumes":[{"source":"` + filepath.Join(root, "projects/blog/data") + `","target":"/data"}]}`
	if _, err := run(t, e, "docker.create", ok); err != nil {
		t.Errorf("an ordinary workload must still be creatable: %v", err)
	}
	if _, err := run(t, e, "docker.remove", `{"name":"nodera-blog"}`); err != nil {
		t.Errorf("an ordinary workload must still be removable: %v", err)
	}
}

func TestLabelledControlPlaneContainerIsProtected(t *testing.T) {
	e, _, h := newExec(t)
	// A container that exists on the host with the control-plane label, under a name the lists do not know.
	if _, _, err := h.Containers.Create(context.Background(), mockSpec("legacy-api", map[string]string{guard.RoleLabel: guard.RoleControl})); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, e, "docker.stop", `{"name":"legacy-api"}`); err == nil {
		t.Fatal("label must protect the container")
	}
}

func TestDatabaseDumpAndLoadUseFilesInsideRoot(t *testing.T) {
	e, root, _ := newExec(t)
	pw := "abcdefghijklmnop1234"
	if _, err := run(t, e, "database.ensure", `{"name":"wp_a","user":"u_a","password":"`+pw+`"}`); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".tmp"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, e, "database.dump", `{"name":"wp_a","path":".tmp/a.sql"}`); err != nil {
		t.Fatalf("dump: %v", err)
	}
	if st, err := os.Stat(filepath.Join(root, ".tmp/a.sql")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("dump file must exist with 0600: %v %v", st, err)
	}
	if _, err := run(t, e, "database.load", `{"name":"wp_a","path":".tmp/a.sql"}`); err != nil {
		t.Fatalf("load: %v", err)
	}
	// Existing dump files are never overwritten (O_EXCL) and traversal is rejected.
	if _, err := run(t, e, "database.dump", `{"name":"wp_a","path":".tmp/a.sql"}`); err == nil {
		t.Error("dump must not overwrite an existing file")
	}
	for _, p := range []string{`../x.sql`, `/etc/x.sql`} {
		if _, err := run(t, e, "database.dump", `{"name":"wp_a","path":"`+p+`"}`); err == nil {
			t.Errorf("path %q must be rejected", p)
		}
	}
	// Identifier injection is rejected by the allowlist.
	if _, err := run(t, e, "database.drop", "{\"name\":\"a`; DROP DATABASE mysql;--\",\"user\":\"u\"}"); err == nil {
		t.Error("injection in name must be rejected")
	}
}

func mockSpec(name string, labels map[string]string) providers.ContainerSpec {
	return providers.ContainerSpec{Name: name, Image: "alpine", Labels: labels}
}
