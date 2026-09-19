// Package config loads Nodera's runtime configuration from environment
// variables. No secret ever has a hardcoded default; anything sensitive is
// required and the process fails fast at startup if it is missing.
package config

import (
	"fmt"
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
}

type HTTPConfig struct {
	Addr string
	// CORSOrigins is the allow-list of origins permitted to call this API
	// from a browser (the web/ frontend, in dev and eventually production).
	// Empty means no cross-origin browser access is permitted — same-origin
	// and non-browser (curl, server-to-server) callers are unaffected, since
	// CORS is a browser-enforced mechanism, not a server-side access check.
	CORSOrigins []string
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

	dbURL := os.Getenv("NODERA_DATABASE_URL")
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
			Addr:        getEnvDefault("NODERA_HTTP_ADDR", ":8080"),
			CORSOrigins: getEnvList("NODERA_CORS_ORIGINS", []string{"http://localhost:3000"}),
		},
		DB: DBConfig{
			URL:             dbURL,
			MaxConns:        maxConns,
			ConnMaxLifetime: time.Hour,
		},
		Redis: RedisConfig{
			URL: os.Getenv("NODERA_REDIS_URL"),
		},
		Auth: AuthConfig{
			SessionTokenBytes: 32,
			SessionTTL:        30 * 24 * time.Hour,
		},
		Secrets: SecretsConfig{
			EncryptionKeyBase64: os.Getenv("NODERA_SECRETS_ENCRYPTION_KEY"),
		},
		Ollama: OllamaConfig{
			BaseURL: os.Getenv("NODERA_OLLAMA_BASE_URL"),
		},
		Anthropic: AnthropicConfig{
			APIKey: os.Getenv("NODERA_ANTHROPIC_API_KEY"),
		},
		OpenAI: OpenAIConfig{
			APIKey: os.Getenv("NODERA_OPENAI_API_KEY"),
		},
		Platform: PlatformConfig{
			BootstrapAdminEmail: os.Getenv("NODERA_PLATFORM_BOOTSTRAP_ADMIN_EMAIL"),
		},
	}

	return cfg, nil
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
