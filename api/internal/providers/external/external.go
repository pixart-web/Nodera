// Package external holds the PREPARED adapters for third-party services Nodera
// will integrate with on the production environment: Cloudflare (DNS), Let's
// Encrypt (ACME certificates), Hetzner Cloud (nodes) and GitHub (git). They
// implement the provider interfaces so wiring is a one-line change later, but
// they are deliberately NOT functional: every method fails with
// providers.ErrUnavailable and a message naming exactly what is missing. They
// are never registered by the server (see cmd/server/wiring.go), so a
// misconfiguration cannot make the platform pretend an external call worked.
package external

import (
	"context"
	"fmt"
	"io"

	"github.com/nodera/nodera/internal/providers"
)

func unavailable(service, need string) error {
	return fmt.Errorf("%w: the %s adapter is not implemented yet (needs %s)", providers.ErrUnavailable, service, need)
}

// ---- Cloudflare DNS ----

// CloudflareDNS will need: NODERA_CLOUDFLARE_API_TOKEN (Zone:Read, DNS:Edit).
type CloudflareDNS struct{ APIToken string }

func (CloudflareDNS) err() error {
	return unavailable("Cloudflare DNS", "a Cloudflare API token and the REST client")
}
func (c CloudflareDNS) EnsureZone(context.Context, string) error { return c.err() }
func (c CloudflareDNS) ListRecords(context.Context, string) ([]providers.DNSRecord, error) {
	return nil, c.err()
}
func (c CloudflareDNS) UpsertRecord(context.Context, string, providers.DNSRecord) (providers.DNSRecord, error) {
	return providers.DNSRecord{}, c.err()
}
func (c CloudflareDNS) DeleteRecord(context.Context, string, string) error { return c.err() }
func (c CloudflareDNS) CheckPropagation(context.Context, string, providers.DNSRecord) (bool, error) {
	return false, c.err()
}

// ---- Let's Encrypt ----

// LetsEncrypt will need: an ACME account key, a reachable HTTP-01 or DNS-01 challenge solver.
type LetsEncrypt struct{ Email string }

func (LetsEncrypt) Name() string { return "letsencrypt" }
func (LetsEncrypt) err() error {
	return unavailable("Let's Encrypt", "an ACME client, an account key and a challenge solver")
}
func (l LetsEncrypt) Issue(context.Context, providers.IssueRequest) (providers.CertBundle, error) {
	return providers.CertBundle{}, l.err()
}
func (l LetsEncrypt) Renew(context.Context, providers.IssueRequest) (providers.CertBundle, error) {
	return providers.CertBundle{}, l.err()
}
func (l LetsEncrypt) Revoke(context.Context, string) error { return l.err() }
func (l LetsEncrypt) Inspect(context.Context, []byte) (providers.CertInfo, error) {
	return providers.CertInfo{}, l.err()
}

// ---- Hetzner Cloud ----

// HetznerNodes will need: NODERA_HETZNER_API_TOKEN (read/write for the project).
type HetznerNodes struct{ APIToken string }

func (HetznerNodes) err() error {
	return unavailable("Hetzner Cloud", "a Hetzner API token and the Cloud API client")
}
func (h HetznerNodes) Describe(context.Context, string) (providers.NodeInfo, error) {
	return providers.NodeInfo{}, h.err()
}
func (h HetznerNodes) Reboot(context.Context, string) error { return h.err() }

// ---- GitHub ----

// GitHub will need: a GitHub App or fine-grained token (stored in internal/secrets), and git/tarball fetch.
type GitHub struct{ Token string }

func (GitHub) err() error { return unavailable("GitHub", "a GitHub token and the REST/tarball client") }
func (g GitHub) ListRepositories(context.Context) ([]providers.Repository, error) {
	return nil, g.err()
}
func (g GitHub) ListBranches(context.Context, string) ([]string, error) { return nil, g.err() }
func (g GitHub) ListCommits(context.Context, string, string, int) ([]providers.Commit, error) {
	return nil, g.err()
}
func (g GitHub) Fetch(context.Context, string, string, providers.FilesystemProvider, string) (string, error) {
	return "", g.err()
}
func (g GitHub) CreateWebhook(context.Context, string, string) (string, error) { return "", g.err() }

var (
	_ providers.DNSProvider         = CloudflareDNS{}
	_ providers.CertificateProvider = LetsEncrypt{}
	_ providers.NodeProvider        = HetznerNodes{}
	_ providers.GitProvider         = GitHub{}
	_                               = io.Discard
)
