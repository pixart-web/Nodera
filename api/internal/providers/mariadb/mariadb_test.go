package mariadb

import (
	"context"
	"io"
	"strings"
	"testing"
)

type call struct {
	env   []string
	stdin string
	args  []string
}

type fakeRunner struct {
	calls  []call
	answer string
}

func (f *fakeRunner) Run(_ context.Context, env []string, stdin io.Reader, stdout io.Writer, args ...string) error {
	c := call{env: env, args: args}
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		c.stdin = string(b)
	}
	f.calls = append(f.calls, c)
	if stdout != nil {
		_, _ = io.WriteString(stdout, f.answer)
	}
	return nil
}

const pw = "Zk3x_9aB-qWe7Rt2Yu1Io"

func TestEnsureDatabase_CreatesOnlyWhenMissingAndKeepsSecretsOutOfArgv(t *testing.T) {
	f := &fakeRunner{answer: "0\n"}
	p, err := NewWithRunner(f, "mariadb-prod", "ROOT-SECRET-123")
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.EnsureDatabase(context.Background(), "wp_site_ab12", "u_ab12", pw)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if len(f.calls) != 2 || !strings.Contains(f.calls[1].stdin, "CREATE DATABASE `wp_site_ab12`") || !strings.Contains(f.calls[1].stdin, "IDENTIFIED BY '"+pw+"'") {
		t.Fatalf("unexpected SQL: %+v", f.calls)
	}
	for _, c := range f.calls {
		joined := strings.Join(c.args, " ")
		if strings.Contains(joined, pw) || strings.Contains(joined, "ROOT-SECRET-123") {
			t.Fatalf("a secret leaked into argv: %s", joined)
		}
		if !strings.Contains(joined, "-e MYSQL_PWD") || len(c.env) != 1 || c.env[0] != "MYSQL_PWD=ROOT-SECRET-123" {
			t.Fatalf("root password must travel by environment only: args=%s env=%v", joined, c.env)
		}
	}
	// An existing database is reused and never altered.
	f2 := &fakeRunner{answer: "1\n"}
	p2, _ := NewWithRunner(f2, "mariadb-prod", "x")
	created, err = p2.EnsureDatabase(context.Background(), "wp_site_ab12", "u_ab12", pw)
	if err != nil || created || len(f2.calls) != 1 {
		t.Fatalf("existing db must be left alone: created=%v err=%v calls=%d", created, err, len(f2.calls))
	}
}

func TestIdentifiersAndPasswordsAreValidated(t *testing.T) {
	f := &fakeRunner{}
	p, _ := NewWithRunner(f, "mariadb-prod", "x")
	ctx := context.Background()
	for _, bad := range []string{"a; DROP DATABASE mysql", "a`b", "a'b", "a b", "", "../x", strings.Repeat("a", 49), "a-b"} {
		if _, err := p.EnsureDatabase(ctx, bad, "u", pw); err == nil {
			t.Errorf("database name %q must be rejected", bad)
		}
		if _, err := p.EnsureDatabase(ctx, "ok", bad, pw); err == nil {
			t.Errorf("user name %q must be rejected", bad)
		}
		if err := p.DropDatabase(ctx, bad, "u"); err == nil {
			t.Errorf("drop %q must be rejected", bad)
		}
		if err := p.Dump(ctx, bad, io.Discard); err == nil {
			t.Errorf("dump %q must be rejected", bad)
		}
		if err := p.Load(ctx, bad, strings.NewReader("")); err == nil {
			t.Errorf("load %q must be rejected", bad)
		}
	}
	for _, badPw := range []string{"short", "has'quote-0123456789", "semi;colon-0123456789", "new\nline-0123456789", "back`tick-0123456789"} {
		if _, err := p.EnsureDatabase(ctx, "ok", "u", badPw); err == nil {
			t.Errorf("password %q must be rejected", badPw)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("nothing may be executed for invalid input, got %d calls", len(f.calls))
	}
	if _, err := NewWithRunner(f, "bad name; rm", "x"); err == nil {
		t.Fatal("invalid container name must be rejected")
	}
	if _, err := NewWithRunner(f, "ok", ""); err == nil {
		t.Fatal("empty root password must be rejected")
	}
}

func TestDumpAndLoadUseTheClientToolsWithoutShell(t *testing.T) {
	f := &fakeRunner{answer: "-- dump --\n"}
	p, _ := NewWithRunner(f, "mariadb-prod", "root-pw")
	var out strings.Builder
	if err := p.Dump(context.Background(), "wp_a", &out); err != nil || out.String() != "-- dump --\n" {
		t.Fatalf("dump: %q %v", out.String(), err)
	}
	if got := strings.Join(f.calls[0].args, " "); !strings.Contains(got, "mariadb-dump -uroot --single-transaction") || !strings.HasSuffix(got, " wp_a") {
		t.Fatalf("dump argv: %s", got)
	}
	if err := p.Load(context.Background(), "wp_a", strings.NewReader("SELECT 1;")); err != nil {
		t.Fatal(err)
	}
	if f.calls[1].stdin != "SELECT 1;" || !strings.Contains(strings.Join(f.calls[1].args, " "), "exec -i -e MYSQL_PWD mariadb-prod mariadb -uroot") {
		t.Fatalf("load call: %+v", f.calls[1])
	}
}
