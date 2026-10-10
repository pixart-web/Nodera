package mock

import (
	"context"
	"errors"
	"testing"

	"github.com/nodera/nodera/internal/providers"
)

func TestContainers_IdempotentCreateAndFaultInjection(t *testing.T) {
	set, h := NewSet()
	ctx := context.Background()
	_, created, _ := set.Containers.Create(ctx, providers.ContainerSpec{Name: "a", Image: "x"})
	_, created2, _ := set.Containers.Create(ctx, providers.ContainerSpec{Name: "a", Image: "x"})
	if !created || created2 {
		t.Fatalf("created=%v created2=%v", created, created2)
	}
	h.Faults.FailOnce("container.start", errors.New("boom"))
	if err := set.Containers.Start(ctx, "a"); err == nil {
		t.Fatal("expected injected failure")
	}
	if err := set.Containers.Start(ctx, "a"); err != nil {
		t.Fatalf("FailOnce must clear itself: %v", err)
	}
	if h.Faults.CallCount("container.start") != 2 {
		t.Fatal("calls must be recorded")
	}
}

func TestDB_EnsureDatabaseDetectsExisting(t *testing.T) {
	set, _ := NewSet()
	c1, _ := set.DB.EnsureDatabase(context.Background(), "db", "u", "p")
	c2, _ := set.DB.EnsureDatabase(context.Background(), "db", "u", "p")
	if !c1 || c2 {
		t.Fatal("second EnsureDatabase must report reuse")
	}
}
