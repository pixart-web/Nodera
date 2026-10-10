package local

import (
	"path/filepath"

	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers"
)

// NewSet builds the local provider set under dataDir. Capabilities that need
// a real runtime (containers, databases, git, machine provisioning) are left
// nil on purpose: engines then fail with a clear "provider not configured"
// instead of pretending. Plug docker.Provider or mock providers in as needed.
func NewSet(dataDir string, monitoringPolicy netpolicy.Policy) (providers.Set, error) {
	fs, err := NewFS(filepath.Join(dataDir, "fs"))
	if err != nil {
		return providers.Set{}, err
	}
	backups, err := NewBackups(filepath.Join(dataDir, "backups"), fs)
	if err != nil {
		return providers.Set{}, err
	}
	certs, err := NewCerts()
	if err != nil {
		return providers.Set{}, err
	}
	return providers.Set{FS: fs, Backups: backups, Certs: certs, DNS: NewDNS(), Monitoring: NewMonitoring(monitoringPolicy)}, nil
}
