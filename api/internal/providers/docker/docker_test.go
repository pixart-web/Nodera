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
