// Package guard classifies protected control-plane resources and refuses agent
// operations that would damage them. Ordinary Nodera-managed application
// operations (provisioning, redeploy, delete) travel the same agent path as
// everything else, so without this a project whose slug happened to equal a
// control-plane container name could stop or remove Nodera itself.
//
// The rule is deliberately a single classification point, not string checks
// scattered through handlers:
//
//   - a container is protected if its name starts with ReservedPrefix, is in
//     the operator-supplied list (Traefik, the shared MariaDB, ...), or carries
//     the label nodera.role=control-plane;
//   - mutations (create/start/stop/restart/remove) on protected containers are
//     refused; read-only inspect and logs stay allowed;
//   - container specs may not claim the control-plane label or reserved prefix,
//     may not bind-mount anything outside the data root (so the Docker socket,
//     /etc, /var/lib/docker and the Postgres volume are unreachable), and may
//     not join the host network.
//
// Recovery is never blocked here: the break-glass path is SSH + docker compose
// on the host, which does not go through the agent (docs/DEPLOYMENT.md).
package guard

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// ReservedPrefix is the container-name prefix reserved for the control plane.
	ReservedPrefix = "nodera-cp-"
	RoleLabel      = "nodera.role"
	RoleControl    = "control-plane"
)

// ErrProtected is returned for any operation refused by the policy.
var ErrProtected = errors.New("refused: protected control-plane resource")

type Policy struct {
	// DataRoot is the absolute host directory managed workloads may bind-mount from.
	DataRoot string
	// ProtectedNames are exact container names that are never mutated through the agent.
	ProtectedNames []string
}

func (p Policy) protectedName(name string) bool {
	if strings.HasPrefix(name, ReservedPrefix) {
		return true
	}
	for _, n := range p.ProtectedNames {
		if n != "" && n == name {
			return true
		}
	}
	return false
}

// CheckMutation refuses lifecycle changes to a protected container. labels are
// those of the existing container (nil when it does not exist).
func (p Policy) CheckMutation(name string, labels map[string]string) error {
	if p.protectedName(name) || labels[RoleLabel] == RoleControl {
		return fmt.Errorf("%w: container %q", ErrProtected, name)
	}
	return nil
}

// CheckCreate validates a container spec that managed workloads ask to create.
func (p Policy) CheckCreate(name string, labels map[string]string, networks []string, volumes [][2]string) error {
	if err := p.CheckMutation(name, nil); err != nil {
		return err
	}
	if labels[RoleLabel] == RoleControl {
		return fmt.Errorf("%w: label %s=%s is reserved", ErrProtected, RoleLabel, RoleControl)
	}
	for _, n := range networks {
		if n == "host" {
			return errors.New("refused: host networking is not allowed for managed workloads")
		}
	}
	root := filepath.Clean(p.DataRoot)
	for _, v := range volumes {
		src := v[0]
		if !filepath.IsAbs(src) {
			return fmt.Errorf("refused: volume source %q must be an absolute host path under the data root", src)
		}
		src = filepath.Clean(src)
		if root == "" || root == "." || (src != root && !strings.HasPrefix(src, root+string(filepath.Separator))) {
			return fmt.Errorf("refused: volume source %q is outside the data root", src)
		}
		if strings.Contains(src, "docker.sock") {
			return errors.New("refused: the Docker socket cannot be mounted into managed workloads")
		}
	}
	return nil
}
