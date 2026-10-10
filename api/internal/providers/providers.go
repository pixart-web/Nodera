// Package providers defines the infrastructure abstraction layer. Business
// logic (operations, engines) depends only on these interfaces, never on
// shell commands or vendor SDKs. Implementations:
//
//	providers/mock   deterministic in-memory doubles with failure injection (tests, demo)
//	providers/local  real, self-contained implementations that run on a developer machine
//	providers/docker Docker CLI adapter (no shell; strict argument validation)
//
// Cloud adapters (Hetzner, Cloudflare, Let's Encrypt, GitHub) implement the
// same interfaces and are wired in at deployment time — see docs/NODE-AGENT.md
// and docs/DEPLOYMENT.md for exactly what remains to be connected.
package providers

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned when a named resource does not exist.
var ErrNotFound = errors.New("providers: resource not found")

// ErrUnavailable signals the backing system cannot be reached (distinct from a
// failed operation so callers can retry).
var ErrUnavailable = errors.New("providers: backend unavailable")

// ---- Containers ----

type PortMapping struct {
	Host      int `json:"host"`
	Container int `json:"container"`
}
type VolumeMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}
type ContainerSpec struct {
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	Env      map[string]string `json:"env,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Ports    []PortMapping     `json:"ports,omitempty"`
	Networks []string          `json:"networks,omitempty"`
	Volumes  []VolumeMount     `json:"volumes,omitempty"`
	Cmd      []string          `json:"cmd,omitempty"`
}
type ContainerInfo struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	State     string            `json:"state"` // created | running | stopped | unhealthy
	Labels    map[string]string `json:"labels,omitempty"`
	StartedAt *time.Time        `json:"started_at,omitempty"`
}

// ContainerProvider manages containers. Create MUST be idempotent by name: a
// second Create with the same name and an identical spec returns the existing
// container rather than an error or a duplicate.
type ContainerProvider interface {
	Create(ctx context.Context, spec ContainerSpec) (ContainerInfo, bool, error) // bool: created now (false = reused)
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Restart(ctx context.Context, name string) error
	Remove(ctx context.Context, name string) error
	Inspect(ctx context.Context, name string) (ContainerInfo, bool, error)
	List(ctx context.Context, labels map[string]string) ([]ContainerInfo, error)
	Logs(ctx context.Context, name string, tail int) ([]string, error)
}

// ---- Filesystem ----

type FileInfo struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// FilesystemProvider operates inside a fixed root; every path is relative to it
// and any attempt to escape (.., absolute paths, symlinks) must fail.
type FilesystemProvider interface {
	MkdirAll(ctx context.Context, path string) error
	WriteFile(ctx context.Context, path string, data []byte) error
	ReadFile(ctx context.Context, path string) ([]byte, error)
	Exists(ctx context.Context, path string) (bool, error)
	Remove(ctx context.Context, path string) error // recursive
	List(ctx context.Context, path string) ([]FileInfo, error)
	DiskUsage(ctx context.Context, path string) (int64, error)
	AbsPath(path string) (string, error) // resolves inside the root (for providers that need real paths)
}

// ---- Databases ----

type DatabaseProvider interface {
	// EnsureDatabase is idempotent: it detects an existing database and reuses
	// it (created=false). The password is applied only on creation.
	EnsureDatabase(ctx context.Context, name, user, password string) (created bool, err error)
	DropDatabase(ctx context.Context, name, user string) error
	Exists(ctx context.Context, name string) (bool, error)
	Dump(ctx context.Context, name string, w io.Writer) error
	Load(ctx context.Context, name string, r io.Reader) error
}

// ---- DNS ----

type DNSRecord struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"` // A AAAA CNAME MX TXT CAA
	Name     string `json:"name"`
	Value    string `json:"value"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority,omitempty"`
}
type DNSProvider interface {
	EnsureZone(ctx context.Context, domain string) error
	ListRecords(ctx context.Context, domain string) ([]DNSRecord, error)
	UpsertRecord(ctx context.Context, domain string, rec DNSRecord) (DNSRecord, error)
	DeleteRecord(ctx context.Context, domain, recordID string) error
	// CheckPropagation reports whether the record is observable via resolution.
	CheckPropagation(ctx context.Context, domain string, rec DNSRecord) (bool, error)
}

// ---- Certificates ----

type IssueRequest struct {
	Domains []string
}
type CertBundle struct {
	CertPEM   []byte
	KeyPEM    []byte // callers MUST store this encrypted (internal/secrets) — never plaintext
	Issuer    string
	Serial    string
	NotBefore time.Time
	NotAfter  time.Time
}
type CertInfo struct {
	Issuer    string    `json:"issuer"`
	Serial    string    `json:"serial"`
	Subject   string    `json:"subject"`
	DNSNames  []string  `json:"dns_names"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
}
type CertificateProvider interface {
	Name() string
	Issue(ctx context.Context, req IssueRequest) (CertBundle, error)
	Renew(ctx context.Context, req IssueRequest) (CertBundle, error)
	Revoke(ctx context.Context, serial string) error
	Inspect(ctx context.Context, certPEM []byte) (CertInfo, error)
}

// ---- Backups ----

type BackupSource struct {
	Paths        []string                                     // filesystem paths (relative to the FilesystemProvider root)
	DatabaseDump func(ctx context.Context, w io.Writer) error // optional
}
type BackupArtifact struct {
	Ref    string `json:"ref"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type BackupProvider interface {
	Create(ctx context.Context, id string, src BackupSource) (BackupArtifact, error)
	Verify(ctx context.Context, ref, expectedSHA256 string) error
	// Restore extracts the artifact; dbLoad (optional) receives the database dump.
	Restore(ctx context.Context, ref string, dest FilesystemProvider, dbLoad func(ctx context.Context, r io.Reader) error) error
	Delete(ctx context.Context, ref string) error
}

// ---- Monitoring ----

type CheckResult struct {
	OK        bool           `json:"ok"`
	LatencyMs int            `json:"latency_ms"`
	Detail    string         `json:"detail,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}
type NodeMetrics struct {
	CPUPercent  float64 `json:"cpu_percent"`
	RAMUsedMB   float64 `json:"ram_used_mb"`
	RAMTotalMB  float64 `json:"ram_total_mb"`
	DiskUsedGB  float64 `json:"disk_used_gb"`
	DiskTotalGB float64 `json:"disk_total_gb"`
	NetUpMbps   float64 `json:"net_up_mbps"`
	NetDownMbps float64 `json:"net_down_mbps"`
}
type MonitoringProvider interface {
	CheckHTTP(ctx context.Context, url string, expectStatus int) (CheckResult, error)
	CheckTCP(ctx context.Context, addr string) (CheckResult, error)
	CheckDNS(ctx context.Context, host string) (CheckResult, error)
	CheckSSL(ctx context.Context, host string) (CheckResult, error) // Extra["days_remaining"]
	CollectNodeMetrics(ctx context.Context, nodeRef string) (NodeMetrics, error)
}

// ---- Nodes ----

type NodeInfo struct {
	Ref      string `json:"ref"`
	Hostname string `json:"hostname"`
	Provider string `json:"provider"`
	Status   string `json:"status"`
}

// NodeProvider is the machine-level provider (Hetzner Cloud in production).
type NodeProvider interface {
	Describe(ctx context.Context, ref string) (NodeInfo, error)
	Reboot(ctx context.Context, ref string) error
}

// ---- Git ----

type Repository struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}
type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	At      time.Time `json:"at"`
}
type GitProvider interface {
	ListRepositories(ctx context.Context) ([]Repository, error)
	ListBranches(ctx context.Context, repo string) ([]string, error)
	ListCommits(ctx context.Context, repo, branch string, limit int) ([]Commit, error)
	// Fetch materialises repo@ref into the destination filesystem path and
	// returns the resolved commit SHA.
	Fetch(ctx context.Context, repo, ref string, dest FilesystemProvider, destPath string) (sha string, err error)
	CreateWebhook(ctx context.Context, repo, callbackURL string) (id string, err error)
}

// Set bundles every provider an engine may need. A nil field means the
// capability is not configured; engines must check and fail with a clear
// error rather than panic or fabricate a result.
type Set struct {
	Containers ContainerProvider
	FS         FilesystemProvider
	DB         DatabaseProvider
	DNS        DNSProvider
	Certs      CertificateProvider
	Backups    BackupProvider
	Monitoring MonitoringProvider
	Nodes      NodeProvider
	Git        GitProvider
}
