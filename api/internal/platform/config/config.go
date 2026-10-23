// Package config loads Nodera's runtime configuration from environment
// variables. No secret ever has a hardcoded default; anything sensitive is
// required and the process fails fast at startup if it is missing.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env       string // "development" | "test" | "production"
	HTTP      HTTPConfig
	DB        DBConfig
	Redis     RedisConfig
	Auth      AuthConfig
	Secrets   SecretsConfig
	Ollama    OllamaConfig
	Anthropic AnthropicConfig
	OpenAI    OpenAIConfig
	Platform  PlatformConfig
	Agent     AgentConfig
	Runtime   RuntimeConfig
	Backup    BackupConfig
	Migrate   MigrateConfig
}

// MigrateConfig controls who applies schema migrations.
type MigrateConfig struct {
	// Auto applies pending migrations at API start. Default true outside
	// production and false in production, where the dedicated `nodera-migrate`
	// binary runs first (with the owner role) and the API only verifies that
	// the schema is current.
	Auto bool
}

// BackupConfig selects where backup archives are stored.
type BackupConfig struct {
	// Destination: "local" (archives stay on this host) or "sftp" (staged
	// locally, then uploaded off-site and verified). See docs/BACKUPS.md.
	Destination string
	SFTP        SFTPConfig
}

// SFTPConfig describes an SFTP target such as a Hetzner Storage Box.
// Credentials are never inline: the private key / password are read from files
// (Docker/Compose secrets) so they do not appear in environment dumps.
type SFTPConfig struct {
	Addr           string // host:port (Hetzner Storage Box uses port 23)
	User           string
	Dir            string // remote directory; must already exist
	PrivateKeyFile string
	PasswordFile   string
	HostKeySHA256  string // pinned host key fingerprint, "SHA256:..." (required)
	KeepLocalCopy  bool
	UploadTimeout  time.Duration
}

type HTTPConfig struct {
	Addr string
	// CORSOrigins is the allow-list of origins permitted to call this API
	// from a browser (the web/ frontend, in dev and eventually production).
	// Empty means no cross-origin browser access is permitted — same-origin
	// and non-browser (curl, server-to-server) callers are unaffected, since
	// CORS is a browser-enforced mechanism, not a server-side access check.
	CORSOrigins []string
	// TrustedProxies are the addresses/CIDRs whose X-Forwarded-For is believed
	// (the reverse proxy). Empty = not behind a proxy. Required for correct
	// per-client rate limiting behind Traefik; see docs/DEPLOYMENT.md.
	TrustedProxies []string
}

type DBConfig struct {
	URL             string
	MaxConns        int32
	ConnMaxLifetime time.Duration
}

type RedisConfig struct {
	URL string // empty means Redis-backed features (queues, rate limiting) are disabled
}

type AuthConfig struct {
	// SessionTokenBytes is the number of random bytes used to generate opaque
	// session tokens (see ADR-005). 32 bytes = 256 bits.
	SessionTokenBytes int
	SessionTTL        time.Duration
}

type SecretsConfig struct {
	// EncryptionKeyBase64 is the base64-encoded AES-256 key used by
	// internal/secrets. Empty means the secrets module is disabled — see
	// docs/SECURITY.md and cmd/server/main.go.
	EncryptionKeyBase64 string
}

type OllamaConfig struct {
	// BaseURL, e.g. "http://localhost:11434". Empty means the Ollama
	// provider adapter is not registered — an ai_profiles row referencing
	// "ollama/<model>" simply won't resolve (rule 36), rather than the
	// server failing to start or fabricating a response.
	BaseURL string
}

type AnthropicConfig struct {
	// APIKey is a real secret (unlike OllamaConfig.BaseURL) — treat it with
	// the same care as NODERA_DATABASE_URL: never log it, never commit a
	// real value to .env. Empty means the Anthropic provider adapter is not
	// registered — see docs/AI_ARCHITECTURE.md for why this is an env var
	// rather than internal/secrets in this phase (ai_providers is
	// platform-wide; internal/secrets is org-scoped — a deliberate
	// mismatch not yet resolved).
	APIKey string
}

type OpenAIConfig struct {
	// APIKey is a real secret, same handling as AnthropicConfig.APIKey.
	// Empty means the OpenAI provider adapter is not registered.
	APIKey string
}

// RuntimeConfig selects how infrastructure providers are backed.
type RuntimeConfig struct {
	// GitHubToken authenticates GitHub repository operations. Prefer *_FILE in production via SecretFromEnv.
	GitHubToken string
	// ProviderMode: "local" (default; real local filesystem/backups/certs/DNS
	// simulation, no container/database/git runtime), "docker" (local + Docker
	// CLI containers) or "mock" (everything in-memory, development/demo only —
	// refused in production). See docs/DEVELOPMENT.md.
	ProviderMode  string
	DataDir       string
	DockerBin     string
	DockerNetwork string
	// BaseDomain, when set, makes provisioning add Traefik routing labels.
	BaseDomain string
	// MariaDBContainer, with MariaDBRootPassword, enables the real database
	// provider (docker mode only): an existing MariaDB container the API
	// manages through `docker exec`. WordPressDBHost is the hostname WordPress
	// containers use to reach it on the shared Docker network.
	MariaDBContainer    string
	MariaDBRootPassword string
	WordPressDBHost     string
	// AllowInternalMonitoring lets monitors probe private addresses (dev only).
	AllowInternalMonitoring bool
	// LocalAgentID is the Node Agent (UUID) that executes container and
	// database operations in ProviderMode "agent". Only the platform operator
	// can set it, so a tenant cannot enrol an agent that receives platform work.
	LocalAgentID string
	// WorkloadUser is the "uid:gid" managed containers run as. Default: the API's
	// own uid/gid when it is not root, so site files are owned by the control
	// plane's user. Override with NODERA_WORKLOAD_USER; set "image" to keep each
	// image's default user.
	WorkloadUser string
	// AllowDirectDocker is the explicit acknowledgement required to run
	// ProviderMode "docker" (API holding the Docker socket) in production.
	AllowDirectDocker bool
}

type AgentConfig struct {
	// SigningKeyBase64 is a base64-encoded 32-byte Ed25519 seed used to sign
	// every command sent to Node Agents. Required in production (an
	// ephemeral key would invalidate queued commands on every restart);
	// optional in development, where a throwaway key is generated.
	SigningKeyBase64 string
}

type PlatformConfig struct {
	// BootstrapAdminEmail, if set, grants every platform permission
	// (internal/platformauth) to the user with this email at every
	// startup — idempotent, so it's safe to leave set permanently or only
	// set it once for the first boot. This is the explicit,
	// operator-controlled mechanism for establishing the first platform
	// administrator; see docs/SECURITY.md "Bootstrapping the first
	// platform administrator". A misconfigured/nonexistent email logs a
	// warning rather than failing startup.
	BootstrapAdminEmail string
}

// Load reads configuration from the environment. It returns an error rather
// than panicking so callers (including tests) can handle misconfiguration
// explicitly.
func Load() (Config, error) {
	env := getEnvDefault("NODERA_ENV", "development")

	// Secrets may be supplied as NAME or as NAME_FILE (a path to a file holding
	// the value — Docker/Compose secrets or a root-owned file mounted read-only).
	// The file form keeps them out of `docker inspect` and /proc/<pid>/environ.
	var secretErr error
	secret := func(name string) string {
		v, err := SecretFromEnv(name)
		if err != nil && secretErr == nil {
			secretErr = err
		}
		return v
	}

	dbURL := secret("NODERA_DATABASE_URL")
	if secretErr != nil {
		return Config{}, secretErr
	}
	if dbURL == "" {
		return Config{}, fmt.Errorf("config: NODERA_DATABASE_URL is required")
	}

	maxConns, err := getEnvInt32Default("NODERA_DATABASE_MAX_CONNS", 10)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Env: env,
		HTTP: HTTPConfig{
			Addr:           getEnvDefault("NODERA_HTTP_ADDR", ":8080"),
			CORSOrigins:    getEnvList("NODERA_CORS_ORIGINS", []string{"http://localhost:3000"}),
			TrustedProxies: getEnvList("NODERA_TRUSTED_PROXIES", nil),
		},
		DB: DBConfig{
			URL:             dbURL,
			MaxConns:        maxConns,
			ConnMaxLifetime: time.Hour,
		},
		Redis: RedisConfig{
			URL: secret("NODERA_REDIS_URL"),
		},
		Auth: AuthConfig{
			SessionTokenBytes: 32,
			SessionTTL:        30 * 24 * time.Hour,
		},
		Secrets: SecretsConfig{
			EncryptionKeyBase64: secret("NODERA_SECRETS_ENCRYPTION_KEY"),
		},
		Ollama: OllamaConfig{
			BaseURL: os.Getenv("NODERA_OLLAMA_BASE_URL"),
		},
		Anthropic: AnthropicConfig{
			APIKey: secret("NODERA_ANTHROPIC_API_KEY"),
		},
		OpenAI: OpenAIConfig{
			APIKey: secret("NODERA_OPENAI_API_KEY"),
		},
		Agent: AgentConfig{SigningKeyBase64: secret("NODERA_AGENT_SIGNING_KEY")},
		Runtime: RuntimeConfig{
			ProviderMode:            getEnvDefault("NODERA_PROVIDER_MODE", "local"),
			DataDir:                 getEnvDefault("NODERA_DATA_DIR", "./data"),
			DockerBin:               getEnvDefault("NODERA_DOCKER_BIN", "docker"),
			GitHubToken:             secret("NODERA_GITHUB_TOKEN"),
			DockerNetwork:           os.Getenv("NODERA_DOCKER_NETWORK"),
			BaseDomain:              os.Getenv("NODERA_BASE_DOMAIN"),
			MariaDBContainer:        os.Getenv("NODERA_MARIADB_CONTAINER"),
			MariaDBRootPassword:     secret("NODERA_MARIADB_ROOT_PASSWORD"),
			WordPressDBHost:         getEnvDefault("NODERA_WORDPRESS_DB_HOST", "db"),
			AllowInternalMonitoring: os.Getenv("NODERA_MONITOR_ALLOW_INTERNAL") == "true",
		},
		Platform: PlatformConfig{
			BootstrapAdminEmail: os.Getenv("NODERA_PLATFORM_BOOTSTRAP_ADMIN_EMAIL"),
		},
	}

	if secretErr != nil {
		return Config{}, secretErr
	}
	switch cfg.Runtime.ProviderMode {
	case "local", "docker", "agent", "mock":
	default:
		return Config{}, fmt.Errorf("config: NODERA_PROVIDER_MODE must be local, docker, agent or mock")
	}

	cfg.Migrate.Auto = env != "production"
	if v := os.Getenv("NODERA_AUTO_MIGRATE"); v != "" {
		cfg.Migrate.Auto = v == "true"
	}
	cfg.Backup = BackupConfig{
		Destination: getEnvDefault("NODERA_BACKUP_DESTINATION", "local"),
		SFTP: SFTPConfig{
			Addr:           os.Getenv("NODERA_BACKUP_SFTP_ADDR"),
			User:           os.Getenv("NODERA_BACKUP_SFTP_USER"),
			Dir:            getEnvDefault("NODERA_BACKUP_SFTP_DIR", "nodera-backups"),
			PrivateKeyFile: os.Getenv("NODERA_BACKUP_SFTP_KEY_FILE"),
			PasswordFile:   os.Getenv("NODERA_BACKUP_SFTP_PASSWORD_FILE"),
			HostKeySHA256:  os.Getenv("NODERA_BACKUP_SFTP_HOST_KEY_SHA256"),
			KeepLocalCopy:  os.Getenv("NODERA_BACKUP_KEEP_LOCAL") == "true",
			UploadTimeout:  time.Hour,
		},
	}
	cfg.Runtime.WorkloadUser = getEnvDefault("NODERA_WORKLOAD_USER", defaultWorkloadUser())
	if cfg.Runtime.WorkloadUser == "image" {
		cfg.Runtime.WorkloadUser = ""
	}
	cfg.Runtime.AllowDirectDocker = os.Getenv("NODERA_ALLOW_DIRECT_DOCKER") == "true"
	cfg.Runtime.LocalAgentID = os.Getenv("NODERA_LOCAL_AGENT_ID")

	if err := cfg.validate(os.Getenv("NODERA_CORS_ORIGINS") != ""); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate rejects configurations that are unsafe or inconsistent. The
// production checks exist so a development default can never silently reach a
// real server: anything security-relevant must be set explicitly.
func (c Config) validate(corsExplicit bool) error {
	switch c.Backup.Destination {
	case "local":
	case "sftp":
		s := c.Backup.SFTP
		if s.Addr == "" || s.User == "" || s.HostKeySHA256 == "" {
			return fmt.Errorf("config: NODERA_BACKUP_DESTINATION=sftp requires NODERA_BACKUP_SFTP_ADDR, _USER and _HOST_KEY_SHA256")
		}
		for _, f := range strings.Split(s.HostKeySHA256, ",") {
			f = strings.TrimSpace(f)
			if i := strings.LastIndex(f, " "); i >= 0 {
				f = f[i+1:] // optional leading key type: "ssh-ed25519 SHA256:..."
			}
			if !strings.HasPrefix(f, "SHA256:") {
				return fmt.Errorf("config: NODERA_BACKUP_SFTP_HOST_KEY_SHA256 must be comma-separated pins like \"SHA256:<base64>\" or \"ssh-ed25519 SHA256:<base64>\" (ssh-keygen -lf output)")
			}
		}
		if (s.PrivateKeyFile == "") == (s.PasswordFile == "") {
			return fmt.Errorf("config: set exactly one of NODERA_BACKUP_SFTP_KEY_FILE or NODERA_BACKUP_SFTP_PASSWORD_FILE")
		}
	default:
		return fmt.Errorf("config: NODERA_BACKUP_DESTINATION must be local or sftp")
	}
	if c.Env != "production" {
		return nil
	}
	if c.Runtime.ProviderMode == "mock" {
		return fmt.Errorf("config: NODERA_PROVIDER_MODE=mock is not allowed in production")
	}
	if c.Runtime.ProviderMode == "docker" && !c.Runtime.AllowDirectDocker {
		return fmt.Errorf("config: NODERA_PROVIDER_MODE=docker gives the API the host Docker socket; production uses NODERA_PROVIDER_MODE=agent (set NODERA_ALLOW_DIRECT_DOCKER=true only to override knowingly)")
	}
	if !corsExplicit {
		return fmt.Errorf("config: NODERA_CORS_ORIGINS must be set explicitly in production")
	}
	for _, o := range c.HTTP.CORSOrigins {
		u, err := url.Parse(o)
		if o == "*" || err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" && u.Path != "/" {
			return fmt.Errorf("config: production CORS origin %q must be an exact https:// origin", o)
		}
		h := u.Hostname()
		if h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost") {
			return fmt.Errorf("config: production CORS origin %q must not be localhost", o)
		}
	}
	if len(c.HTTP.TrustedProxies) == 0 {
		return fmt.Errorf("config: NODERA_TRUSTED_PROXIES must be set in production (the reverse proxy's network): without it every client shares one IP-based rate-limit bucket behind the proxy")
	}
	if c.Secrets.EncryptionKeyBase64 == "" || c.Agent.SigningKeyBase64 == "" {
		return fmt.Errorf("config: NODERA_SECRETS_ENCRYPTION_KEY and NODERA_AGENT_SIGNING_KEY are required in production")
	}
	if strings.Contains(c.DB.URL, "nodera_dev_password") {
		return fmt.Errorf("config: the development database password must not be used in production")
	}
	if os.Getenv("NODERA_DEMO_SEED") == "true" {
		return fmt.Errorf("config: NODERA_DEMO_SEED is not allowed in production")
	}
	if c.Runtime.AllowInternalMonitoring {
		return fmt.Errorf("config: NODERA_MONITOR_ALLOW_INTERNAL is a development switch and is not allowed in production")
	}
	return nil
}

func defaultWorkloadUser() string {
	if os.Getuid() <= 0 {
		return "" // running as root (or on a platform without uids): keep the image default
	}
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// SecretFromEnv returns the value of NAME_FILE's file (trailing newline
// trimmed) when NAME_FILE is set, else NAME. Setting both is an error: it is
// ambiguous which one is live. The returned value must never be logged.
func SecretFromEnv(name string) (string, error) {
	file := os.Getenv(name + "_FILE")
	inline := os.Getenv(name)
	if file == "" {
		return inline, nil
	}
	if inline != "" {
		return "", fmt.Errorf("config: set either %s or %s_FILE, not both", name, name)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("config: cannot read %s_FILE (%s): %w", name, file, err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getEnvList reads a comma-separated env var into a trimmed string slice,
// or returns def if the var is unset. Production deployments must set
// NODERA_CORS_ORIGINS explicitly to their real frontend origin(s) — the
// localhost:3000 default only matters to a browser already running on the
// developer's own machine.
func getEnvList(key string, def []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnvInt32Default(key string, def int32) (int32, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("config: invalid value for %s: %w", key, err)
	}
	return int32(n), nil
}
