package main

import (
	"fmt"

	"github.com/nodera/nodera/internal/platform/config"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/docker"
	"github.com/nodera/nodera/internal/providers/local"
	"github.com/nodera/nodera/internal/providers/mariadb"
	"github.com/nodera/nodera/internal/providers/mock"
)

// buildProviders assembles the infrastructure provider set for the configured
// mode. Capabilities a mode cannot honestly provide stay nil, and the engines
// then refuse to run with "provider not configured" rather than simulate.
func buildProviders(cfg config.Config) (providers.Set, error) {
	rt := cfg.Runtime
	switch rt.ProviderMode {
	case "mock":
		set, _ := mock.NewSet()
		return set, nil
	case "local", "docker":
		policy := netpolicy.Policy{Level: netpolicy.PublicOnly}
		if rt.AllowInternalMonitoring {
			policy.Level = netpolicy.InternalAllowed
		}
		set, err := local.NewSet(rt.DataDir, policy)
		if err != nil {
			return providers.Set{}, err
		}
		if rt.ProviderMode == "docker" {
			set.Containers = docker.New(rt.DockerBin)
			if rt.MariaDBContainer != "" {
				db, err := mariadb.New(rt.DockerBin, rt.MariaDBContainer, rt.MariaDBRootPassword)
				if err != nil {
					return providers.Set{}, fmt.Errorf("mariadb provider: %w", err)
				}
				set.DB = db
			}
		}
		return set, nil
	}
	return providers.Set{}, fmt.Errorf("unknown provider mode %q", rt.ProviderMode)
}

// capabilityReport states, per capability, whether it is backed by a real
// implementation, a mock, or is not configured. The UI shows this verbatim so
// nothing ever looks real when it is not.
func capabilityReport(mode string, set providers.Set) map[string]string {
	state := func(present bool, localReal bool) string {
		switch {
		case !present:
			return "not_configured"
		case mode == "mock":
			return "mock"
		case localReal:
			return "local"
		default:
			return "real"
		}
	}
	return map[string]string{
		"containers":   state(set.Containers != nil, false),
		"filesystem":   state(set.FS != nil, true),
		"database":     state(set.DB != nil, false),
		"dns":          state(set.DNS != nil, true),
		"certificates": state(set.Certs != nil, true),
		"backups":      state(set.Backups != nil, true),
		"monitoring":   state(set.Monitoring != nil, true),
		"git":          state(set.Git != nil, false),
		"nodes":        state(set.Nodes != nil, false),
	}
}
