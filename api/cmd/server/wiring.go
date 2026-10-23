package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/nodeagent"
	"github.com/nodera/nodera/internal/nodeagent/remote"
	"github.com/nodera/nodera/internal/platform/config"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/docker"
	"github.com/nodera/nodera/internal/providers/github"
	"github.com/nodera/nodera/internal/providers/local"
	"github.com/nodera/nodera/internal/providers/mariadb"
	"github.com/nodera/nodera/internal/providers/mock"
	"github.com/nodera/nodera/internal/providers/offsite"
)

// buildProviders assembles the infrastructure provider set for the configured
// mode. Capabilities a mode cannot honestly provide stay nil, and the engines
// then refuse to run with "provider not configured" rather than simulate.
func buildProviders(cfg config.Config, agents *nodeagent.Service) (providers.Set, error) {
	rt := cfg.Runtime
	switch rt.ProviderMode {
	case "mock":
		set, _ := mock.NewSet()
		return set, nil
	case "local", "docker", "agent":
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
				if os.Getenv("NODERA_MARIADB_LEGACY_CLIENT") == "true" {
					db.UseLegacyClient()
				}
				set.DB = db
			}
		}
		// GitHub's REST API supports public repositories without credentials; a token is optional for public access and enables private repositories when configured.
		set.Git = github.New(cfg.Runtime.GitHubToken)
		if cfg.Backup.Destination == "sftp" {
			store, err := sftpStore(cfg.Backup.SFTP)
			if err != nil {
				return providers.Set{}, fmt.Errorf("off-site backups: %w", err)
			}
			staging, ok := set.Backups.(*local.Backups)
			if !ok {
				return providers.Set{}, fmt.Errorf("off-site backups: unexpected local backup provider")
			}
			set.Backups = offsite.NewBackups(staging, store, cfg.Backup.SFTP.KeepLocalCopy)
		}
		if rt.ProviderMode == "agent" {
			// The API holds no Docker socket: containers and the database are
			// driven through the one local Node Agent named by the operator.
			// Without an id the capabilities stay absent and engines refuse to
			// run ("provider not configured") instead of pretending.
			if rt.LocalAgentID != "" {
				id, err := uuid.Parse(rt.LocalAgentID)
				if err != nil {
					return providers.Set{}, fmt.Errorf("NODERA_LOCAL_AGENT_ID is not a valid UUID")
				}
				set.Containers, set.DB = remote.LocalNode(agents, id, set.FS)
			}
		}
		return set, nil
	}
	return providers.Set{}, fmt.Errorf("unknown provider mode %q", rt.ProviderMode)
}

// sftpStore builds the off-site store, reading credentials from the files the
// operator mounted (Docker/Compose secrets). Their contents are never logged.
func sftpStore(c config.SFTPConfig) (*offsite.SFTPStore, error) {
	cfg := offsite.SFTPConfig{Addr: c.Addr, User: c.User, Dir: c.Dir, DialTimeout: 30 * time.Second}
	for _, f := range strings.Split(c.HostKeySHA256, ",") {
		cfg.HostKeySHA256 = append(cfg.HostKeySHA256, strings.TrimSpace(f))
	}
	if c.PrivateKeyFile != "" {
		b, err := os.ReadFile(c.PrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read NODERA_BACKUP_SFTP_KEY_FILE: %w", err)
		}
		cfg.PrivateKeyPEM = b
	} else {
		b, err := os.ReadFile(c.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read NODERA_BACKUP_SFTP_PASSWORD_FILE: %w", err)
		}
		cfg.Password = strings.TrimRight(string(b), "\r\n")
	}
	return offsite.NewSFTPStore(cfg)
}

// capabilityReport states, per capability, how it is backed. The UI shows this
// verbatim so nothing ever looks real when it is not:
//
//	real           implemented against the real system by this process (docker CLI, ...)
//	agent          executed through the Node Agent (signed commands); not validated until staging
//	local          real, but on this host's filesystem (files, same-host backups, probes)
//	local_dev      development/test implementation only: DNS records live in memory and
//	               are lost on restart; certificates are self-signed and not browser-trusted
//	mock           simulated in memory
//	not_configured nothing is wired for this capability
func capabilityReport(mode, backupDest string, set providers.Set) map[string]string {
	state := func(present bool, kind string) string {
		switch {
		case !present:
			return "not_configured"
		case mode == "mock":
			return "mock"
		case kind == "" && mode == "agent":
			return "agent"
		case kind == "":
			return "real"
		default:
			return kind
		}
	}
	backups := state(set.Backups != nil, "local")
	if backupDest == "sftp" && set.Backups != nil && mode != "mock" {
		backups = "offsite"
	}
	return map[string]string{
		"containers":   state(set.Containers != nil, func() string { if mode == "agent" { return "agent" }; return "" }()),
		"filesystem":   state(set.FS != nil, "local"),
		"database":     state(set.DB != nil, func() string { if mode == "agent" { return "agent" }; return "" }()),
		"dns":          state(set.DNS != nil, "local_dev"),
		"certificates": state(set.Certs != nil, "local_dev"),
		"backups":      backups,
		"monitoring":   state(set.Monitoring != nil, "local"),
		"git":          state(set.Git != nil, "real"),
		"nodes":        state(set.Nodes != nil, ""),
	}
}

// productionWarnings lists, in plain words, the things that look configured but
// are not production-grade. It is shown to operators in /api/v1/system so a
// development implementation can never be mistaken for the real thing.
func productionWarnings(env, mode, backupDest string, set providers.Set) []string {
	out := []string{}
	if mode == "mock" {
		out = append(out, "Providers are MOCKS: nothing here touches real containers, databases or DNS.")
		return out
	}
	if set.DNS != nil {
		out = append(out, "DNS uses the local in-memory provider: records are lost on restart and are not published to any authoritative DNS server. Manage real DNS at your DNS host.")
	}
	if set.Certs != nil {
		out = append(out, "Certificates are issued by a local development CA: browsers will not trust them. Public TLS comes from Traefik/Let's Encrypt, not from this provider.")
	}
	if set.Backups != nil && backupDest != "sftp" {
		out = append(out, "Backups are stored on this host only (no off-site destination configured): they do not survive loss of the server.")
	}
	return out
}
