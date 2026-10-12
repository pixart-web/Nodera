package main

import (
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/platform/config"
	"github.com/nodera/nodera/internal/providers/mock"
)

func TestCapabilityReportNeverOverstates(t *testing.T) {
	cfg := config.Config{Runtime: config.RuntimeConfig{ProviderMode: "agent", DataDir: t.TempDir()}, Backup: config.BackupConfig{Destination: "local"}}
	set, err := buildProviders(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if set.Containers != nil || set.DB != nil {
		t.Fatal("agent mode without NODERA_LOCAL_AGENT_ID must not wire container/database providers")
	}
	rep := capabilityReport("agent", "local", set)
	want := map[string]string{
		"containers":   "not_configured",
		"database":     "not_configured",
		"dns":          "local_dev", // in-memory records, lost on restart
		"certificates": "local_dev", // self-signed, not browser-trusted
		"backups":      "local",     // same host: not a disaster-recovery copy
		"filesystem":   "local",
	}
	for k, v := range want {
		if rep[k] != v {
			t.Errorf("capability %s = %q, want %q", k, rep[k], v)
		}
	}
	if got := capabilityReport("agent", "sftp", set)["backups"]; got != "offsite" {
		t.Errorf("sftp destination should report offsite, got %q", got)
	}
	// With the agent wired, containers are reported as agent-executed, never "real".
	mset, _ := mock.NewSet()
	if got := capabilityReport("mock", "local", mset)["containers"]; got != "mock" {
		t.Errorf("mock mode must report mock, got %q", got)
	}
}

func TestAgentModeRejectsMalformedAgentID(t *testing.T) {
	cfg := config.Config{Runtime: config.RuntimeConfig{ProviderMode: "agent", DataDir: t.TempDir(), LocalAgentID: "not-a-uuid"}, Backup: config.BackupConfig{Destination: "local"}}
	if _, err := buildProviders(cfg, nil); err == nil {
		t.Fatal("a malformed NODERA_LOCAL_AGENT_ID must stop startup")
	}
}

func TestProductionWarningsNameWhatIsNotProductionGrade(t *testing.T) {
	set, _ := buildProviders(config.Config{Runtime: config.RuntimeConfig{ProviderMode: "local", DataDir: t.TempDir()}, Backup: config.BackupConfig{Destination: "local"}}, nil)
	joined := strings.Join(productionWarnings("production", "local", "local", set), "\n")
	for _, want := range []string{"in-memory", "development CA", "this host only"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings must mention %q, got:\n%s", want, joined)
		}
	}
	if w := productionWarnings("production", "local", "sftp", set); strings.Contains(strings.Join(w, "\n"), "this host only") {
		t.Error("an off-site destination removes the same-host warning")
	}
	mset, _ := mock.NewSet()
	if w := productionWarnings("development", "mock", "local", mset); len(w) != 1 || !strings.Contains(w[0], "MOCKS") {
		t.Errorf("mock mode must say so: %v", w)
	}
}
