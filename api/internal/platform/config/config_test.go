package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prodEnv is a minimal valid production environment; tests mutate one value.
func prodEnv(t *testing.T, over map[string]string) {
	t.Helper()
	base := map[string]string{
		"NODERA_ENV":                    "production",
		"NODERA_DATABASE_URL":           "postgres://app:x@postgres:5432/nodera",
		"NODERA_CORS_ORIGINS":           "https://nodera.example.com",
		"NODERA_SECRETS_ENCRYPTION_KEY": "k",
		"NODERA_AGENT_SIGNING_KEY":      "k",
		"NODERA_PROVIDER_MODE":          "agent",
		"NODERA_TRUSTED_PROXIES":        "172.16.0.0/12",
	}
	for k, v := range over {
		base[k] = v
	}
	for _, k := range []string{"NODERA_DEMO_SEED", "NODERA_MONITOR_ALLOW_INTERNAL", "NODERA_ALLOW_DIRECT_DOCKER", "NODERA_AUTO_MIGRATE", "NODERA_BACKUP_DESTINATION", "NODERA_CORS_ORIGINS", "NODERA_TRUSTED_PROXIES", "NODERA_BACKUP_SFTP_ADDR", "NODERA_BACKUP_SFTP_USER", "NODERA_BACKUP_SFTP_HOST_KEY_SHA256", "NODERA_BACKUP_SFTP_KEY_FILE", "NODERA_BACKUP_SFTP_PASSWORD_FILE"} {
		t.Setenv(k, "")
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
}

func TestProductionValid(t *testing.T) {
	prodEnv(t, nil)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
	if cfg.Migrate.Auto {
		t.Error("auto-migrate must default to false in production")
	}
}

func TestProductionRejections(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"mock provider", map[string]string{"NODERA_PROVIDER_MODE": "mock"}, "mock"},
		{"docker socket in API", map[string]string{"NODERA_PROVIDER_MODE": "docker"}, "Docker socket"},
		{"no trusted proxies", map[string]string{"NODERA_TRUSTED_PROXIES": ""}, "NODERA_TRUSTED_PROXIES"},
		{"cors unset", map[string]string{"NODERA_CORS_ORIGINS": ""}, "explicitly"},
		{"cors localhost", map[string]string{"NODERA_CORS_ORIGINS": "https://localhost"}, "localhost"},
		{"cors http", map[string]string{"NODERA_CORS_ORIGINS": "http://app.example.com"}, "https"},
		{"cors wildcard", map[string]string{"NODERA_CORS_ORIGINS": "*"}, "https"},
		{"cors with path", map[string]string{"NODERA_CORS_ORIGINS": "https://app.example.com/x"}, "https"},
		{"no secrets key", map[string]string{"NODERA_SECRETS_ENCRYPTION_KEY": ""}, "required"},
		{"no signing key", map[string]string{"NODERA_AGENT_SIGNING_KEY": ""}, "required"},
		{"dev db password", map[string]string{"NODERA_DATABASE_URL": "postgres://nodera:nodera_dev_password@db/n"}, "development database password"},
		{"demo seed", map[string]string{"NODERA_DEMO_SEED": "true"}, "DEMO_SEED"},
		{"internal monitoring", map[string]string{"NODERA_MONITOR_ALLOW_INTERNAL": "true"}, "development switch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prodEnv(t, c.env)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestDirectDockerNeedsExplicitOverride(t *testing.T) {
	prodEnv(t, map[string]string{"NODERA_PROVIDER_MODE": "docker", "NODERA_ALLOW_DIRECT_DOCKER": "true"})
	if _, err := Load(); err != nil {
		t.Fatalf("explicit override should be accepted: %v", err)
	}
}

func TestDevelopmentDefaultsStayConvenient(t *testing.T) {
	prodEnv(t, map[string]string{"NODERA_ENV": "development", "NODERA_PROVIDER_MODE": "local", "NODERA_CORS_ORIGINS": ""})
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Migrate.Auto || len(cfg.HTTP.CORSOrigins) != 1 {
		t.Errorf("development defaults changed: %+v", cfg.Migrate)
	}
}

func TestSFTPBackupConfig(t *testing.T) {
	prodEnv(t, map[string]string{"NODERA_BACKUP_DESTINATION": "sftp"})
	if _, err := Load(); err == nil {
		t.Fatal("sftp without addr/user/host key must be rejected")
	}
	prodEnv(t, map[string]string{"NODERA_BACKUP_DESTINATION": "sftp", "NODERA_BACKUP_SFTP_ADDR": "h:23", "NODERA_BACKUP_SFTP_USER": "u",
		"NODERA_BACKUP_SFTP_HOST_KEY_SHA256": "SHA256:abc", "NODERA_BACKUP_SFTP_KEY_FILE": "/run/secrets/k", "NODERA_BACKUP_SFTP_PASSWORD_FILE": "/run/secrets/p"})
	if _, err := Load(); err == nil {
		t.Fatal("both key and password must be rejected")
	}
	prodEnv(t, map[string]string{"NODERA_BACKUP_DESTINATION": "sftp", "NODERA_BACKUP_SFTP_ADDR": "h:23", "NODERA_BACKUP_SFTP_USER": "u",
		"NODERA_BACKUP_SFTP_HOST_KEY_SHA256": "abc", "NODERA_BACKUP_SFTP_KEY_FILE": "/run/secrets/k"})
	if _, err := Load(); err == nil {
		t.Fatal("unprefixed host key fingerprint must be rejected")
	}
	prodEnv(t, map[string]string{"NODERA_BACKUP_DESTINATION": "sftp", "NODERA_BACKUP_SFTP_ADDR": "h:23", "NODERA_BACKUP_SFTP_USER": "u",
		"NODERA_BACKUP_SFTP_HOST_KEY_SHA256": "ssh-ed25519 SHA256:abc, SHA256:def", "NODERA_BACKUP_SFTP_KEY_FILE": "/run/secrets/k"})
	if _, err := Load(); err != nil {
		t.Fatalf("valid sftp config rejected: %v", err)
	}
}

func TestSecretsCanComeFromFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, v string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(v+"\n"), 0o400); err != nil {
			t.Fatal(err)
		}
		return p
	}
	prodEnv(t, map[string]string{"NODERA_SECRETS_ENCRYPTION_KEY": ""})
	t.Setenv("NODERA_SECRETS_ENCRYPTION_KEY_FILE", write("enc", "file-value"))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Secrets.EncryptionKeyBase64 != "file-value" {
		t.Errorf("file secret not read (or newline not trimmed): %q", cfg.Secrets.EncryptionKeyBase64)
	}
	// Both forms at once is ambiguous and refused.
	prodEnv(t, nil)
	t.Setenv("NODERA_SECRETS_ENCRYPTION_KEY_FILE", write("enc2", "x"))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("both forms must be refused, got %v", err)
	}
	// A missing file fails loudly, and the message names the path, not a value.
	prodEnv(t, map[string]string{"NODERA_SECRETS_ENCRYPTION_KEY": ""})
	t.Setenv("NODERA_SECRETS_ENCRYPTION_KEY_FILE", filepath.Join(dir, "missing"))
	if _, err := Load(); err == nil {
		t.Error("a missing secret file must fail startup")
	}
}
