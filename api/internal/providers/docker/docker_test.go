package docker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/providers"
)

type fakeRunner struct {
	calls  [][]string
	exists bool
	image  string
}

func (f *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	switch args[0] {
	case "inspect":
		if !f.exists {
			return "", fmt.Errorf("docker inspect: Error: No such container")
		}
		return `[{"Id":"abc","Name":"/web","Config":{"Image":"` + f.image + `","Labels":{"nodera.project":"p1"}},"State":{"Status":"running","StartedAt":"2026-01-01T00:00:00Z"}}]`, nil
	case "create":
		f.exists, f.image = true, args[len(args)-1]
	}
	return "", nil
}

func TestCreate_IsIdempotentAndBuildsArgvWithoutShell(t *testing.T) {
	f := &fakeRunner{}
	p := NewWithRunner(f)
	spec := providers.ContainerSpec{Name: "web", Image: "nginx:1.27", Env: map[string]string{"A": "b;rm -rf /"}, Networks: []string{"proxy-public"}}
	_, created, err := p.Create(context.Background(), spec)
	if err != nil || !created {
		t.Fatalf("expected creation, got created=%v err=%v", created, err)
	}
	var createArgs []string
	for _, c := range f.calls {
		if c[0] == "create" {
			createArgs = c
		}
	}
	// A shell metacharacter in a VALUE is passed as a single argv element, never interpreted.
	if !contains(createArgs, "A=b;rm -rf /") {
		t.Fatalf("env value must be a single argv element: %v", createArgs)
	}
	_, created, err = p.Create(context.Background(), spec)
	if err != nil || created {
		t.Fatalf("second Create must reuse (created=%v err=%v)", created, err)
	}
	creates := 0
	for _, c := range f.calls {
		if c[0] == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("expected exactly one docker create, got %d", creates)
	}
}

func TestValidation_RejectsInjection(t *testing.T) {
	p := NewWithRunner(&fakeRunner{})
	bad := []providers.ContainerSpec{
		{Name: "web; rm -rf /", Image: "nginx"},
		{Name: "web", Image: "--privileged"},
		{Name: "web", Image: "nginx", Env: map[string]string{"A B": "x"}},
		{Name: "web", Image: "nginx", Volumes: []providers.VolumeMount{{Source: "/a:/etc", Target: "/x"}}},
		{Name: "web", Image: "nginx", Networks: []string{"host --privileged"}},
	}
	for i, spec := range bad {
		if _, _, err := p.Create(context.Background(), spec); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
	if err := p.Start(context.Background(), "x y"); err == nil {
		t.Error("expected invalid name to be rejected on Start")
	}
}

func TestCreate_RefusesImageMismatch(t *testing.T) {
	f := &fakeRunner{exists: true, image: "nginx:1.26"}
	_, _, err := NewWithRunner(f).Create(context.Background(), providers.ContainerSpec{Name: "web", Image: "nginx:1.27"})
	if err == nil {
		t.Fatal("expected an image-mismatch error rather than silently reusing a different image")
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

var _ = strings.TrimSpace

func TestCreate_UserMustBeNumericAndNeverRoot(t *testing.T) {
	for _, bad := range []string{"root", "0", "00:5", "0:0", "www-data", "1000:root", "1000;id", "-1", "1:2:3"} {
		p := NewWithRunner(&fakeRunner{})
		if _, _, err := p.Create(context.Background(), providers.ContainerSpec{Name: "w", Image: "nginx", User: bad}); err == nil {
			t.Errorf("user %q must be rejected", bad)
		}
	}
	f := &fakeRunner{}
	if _, _, err := NewWithRunner(f).Create(context.Background(), providers.ContainerSpec{Name: "w", Image: "nginx", User: "10001:10001"}); err != nil {
		t.Fatal(err)
	}
	var create []string
	for _, c := range f.calls {
		if c[0] == "create" {
			create = c
		}
	}
	if i := indexOf(create, "--user"); i < 0 || create[i+1] != "10001:10001" {
		t.Fatalf("expected --user 10001:10001, got %v", create)
	}
}

func TestCreate_VolumeSourceMustBeAbsoluteHostPath(t *testing.T) {
	for _, src := range []string{"nodera_postgres_data", "projects/blog/data", "./x"} {
		p := NewWithRunner(&fakeRunner{})
		_, _, err := p.Create(context.Background(), providers.ContainerSpec{Name: "w", Image: "nginx", Volumes: []providers.VolumeMount{{Source: src, Target: "/data"}}})
		if err == nil {
			t.Errorf("volume source %q (a named volume) must be rejected", src)
		}
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
