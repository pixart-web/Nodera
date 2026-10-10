// Package mariadb implements providers.DatabaseProvider for an EXISTING
// MariaDB server running in a Docker container (the Hetzner setup), by
// running the MariaDB client inside that container through `docker exec`.
//
// Safety: no shell is ever used (argv arrays only). Identifiers and the
// generated password are validated against strict character sets before they
// are placed in SQL, SQL travels on stdin (never in argv), and the root
// password travels only through the environment (MYSQL_PWD, forwarded by name),
// so none of it appears in a process list. Exercised through the Runner seam;
// running it against the real server is part of the Hetzner integration step.
package mariadb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/nodera/nodera/internal/providers"
)

// Runner executes `docker <args>` with optional stdin, streaming stdout.
type Runner interface {
	Run(ctx context.Context, env []string, stdin io.Reader, stdout io.Writer, args ...string) error
}

type execRunner struct{ bin string }

func (r execRunner) Run(ctx context.Context, env []string, stdin io.Reader, stdout io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, r.bin, args...) // #nosec G204 -- argv only; identifiers validated by the provider
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker exec: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}

type Provider struct {
	r         Runner
	container string
	rootPass  string
}

// New talks to the MariaDB container through the docker CLI at dockerBin.
func New(dockerBin, container, rootPassword string) (*Provider, error) {
	if dockerBin == "" {
		dockerBin = "docker"
	}
	return NewWithRunner(execRunner{bin: dockerBin}, container, rootPassword)
}

func NewWithRunner(r Runner, container, rootPassword string) (*Provider, error) {
	if !containerRe.MatchString(container) {
		return nil, fmt.Errorf("invalid MariaDB container name %q", container)
	}
	if rootPassword == "" {
		return nil, fmt.Errorf("MariaDB root password is required")
	}
	return &Provider{r: r, container: container, rootPass: rootPassword}, nil
}

var (
	containerRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	identRe     = regexp.MustCompile(`^[A-Za-z0-9_]{1,48}$`)
	passRe      = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
)

func (p *Provider) env() []string { return []string{"MYSQL_PWD=" + p.rootPass} }

func (p *Provider) base(interactive bool, tool string, extra ...string) []string {
	a := []string{"exec"}
	if interactive {
		a = append(a, "-i")
	}
	a = append(a, "-e", "MYSQL_PWD", p.container, tool, "-uroot")
	return append(a, extra...)
}

func (p *Provider) sql(ctx context.Context, query string) (string, error) {
	var out bytes.Buffer
	err := p.r.Run(ctx, p.env(), strings.NewReader(query), &out, p.base(true, "mariadb", "--batch", "--skip-column-names")...)
	return strings.TrimSpace(out.String()), err
}

func (p *Provider) Exists(ctx context.Context, name string) (bool, error) {
	if !identRe.MatchString(name) {
		return false, fmt.Errorf("invalid database name %q", name)
	}
	out, err := p.sql(ctx, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='"+name+"';")
	if err != nil {
		return false, err
	}
	return out == "1", nil
}

// EnsureDatabase is idempotent: an existing database is reused untouched
// (created=false) and the password is applied only when it is created.
func (p *Provider) EnsureDatabase(ctx context.Context, name, user, password string) (bool, error) {
	if !identRe.MatchString(name) || !identRe.MatchString(user) {
		return false, fmt.Errorf("invalid database or user name")
	}
	if !passRe.MatchString(password) {
		return false, fmt.Errorf("password must be 16-128 characters of [A-Za-z0-9_-]")
	}
	if ok, err := p.Exists(ctx, name); err != nil {
		return false, err
	} else if ok {
		return false, nil
	}
	q := "CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n" +
		"CREATE USER IF NOT EXISTS '" + user + "'@'%' IDENTIFIED BY '" + password + "';\n" +
		"ALTER USER '" + user + "'@'%' IDENTIFIED BY '" + password + "';\n" +
		"GRANT ALL PRIVILEGES ON `" + name + "`.* TO '" + user + "'@'%';\nFLUSH PRIVILEGES;\n"
	_, err := p.sql(ctx, q)
	return err == nil, err
}

func (p *Provider) DropDatabase(ctx context.Context, name, user string) error {
	if !identRe.MatchString(name) || !identRe.MatchString(user) {
		return fmt.Errorf("invalid database or user name")
	}
	_, err := p.sql(ctx, "DROP DATABASE IF EXISTS `"+name+"`;\nDROP USER IF EXISTS '"+user+"'@'%';\nFLUSH PRIVILEGES;\n")
	return err
}

func (p *Provider) Dump(ctx context.Context, name string, w io.Writer) error {
	if !identRe.MatchString(name) {
		return fmt.Errorf("invalid database name %q", name)
	}
	return p.r.Run(ctx, p.env(), nil, w, p.base(false, "mariadb-dump", "--single-transaction", "--routines", "--triggers", "--default-character-set=utf8mb4", name)...)
}

func (p *Provider) Load(ctx context.Context, name string, r io.Reader) error {
	if !identRe.MatchString(name) {
		return fmt.Errorf("invalid database name %q", name)
	}
	return p.r.Run(ctx, p.env(), r, io.Discard, p.base(true, "mariadb", "--default-character-set=utf8mb4", name)...)
}

var _ providers.DatabaseProvider = (*Provider)(nil)
