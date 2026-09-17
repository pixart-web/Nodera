// Package config loads Nodera's runtime configuration from environment
// variables. No secret ever has a hardcoded default; anything sensitive is
// required and the process fails fast at startup if it is missing.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env     string // "development" | "test" | "production"
	HTTP    HTTPConfig
	DB      DBConfig
	Redis   RedisConfig
	Auth    AuthConfig
	Secrets SecretsConfig
}

type HTTPConfig struct {
	Addr string
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
			Addr: getEnvDefault("NODERA_HTTP_ADDR", ":8080"),
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
	}

	return cfg, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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
