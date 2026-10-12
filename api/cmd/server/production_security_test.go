package main

// Browser-security posture in PRODUCTION mode (secureCookies on, exact CORS
// origin). Complements cookie_csrf_test.go, which runs with dev settings.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/platform/httpserver"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/platformauth"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/tenancy"
	"github.com/nodera/nodera/internal/testhelpers"
)

func newProdServer(t *testing.T) *httptest.Server {
	t.Helper()
	pool := testhelpers.RequirePool(t)
	auditSvc := audit.New(pool)
	rbacSvc := rbac.New(pool, auditSvc)
	identitySvc := identity.New(pool, rbacSvc, 24*time.Hour, auditSvc)
	deps := apiDeps{
		log: logger.New("production"), identity: identitySvc, tenancy: tenancy.New(pool, identitySvc, auditSvc),
		audit: auditSvc, rbac: rbacSvc, platform: platformauth.New(pool, auditSvc), pool: pool,
		corsOrigins: []string{"https://nodera.example.com"}, sessionTTL: 24 * time.Hour, secureCookies: true,
		environment: "production",
	}
	srv := httptest.NewServer(newRouter(deps))
	t.Cleanup(srv.Close)
	return srv
}

func TestProductionCookiesAreSecureHttpOnlyLaxAndScopedToRoot(t *testing.T) {
	srv := newProdServer(t)
	email := "prod-cookie-" + strings.ReplaceAll(time.Now().Format("150405.000"), ".", "") + "@nodera.dev"
	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse battery staple 9", "display_name": "P"})
	r, _ := http.Post(srv.URL+"/api/v1/auth/signup", "application/json", bytes.NewReader(body))
	r.Body.Close()
	login, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse battery staple 9"})
	resp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(login))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	seen := map[string]*http.Cookie{}
	for _, c := range resp.Cookies() {
		seen[c.Name] = c
	}
	sess, csrf := seen[httpserver.SessionCookieName], seen[httpserver.CSRFCookieName]
	if sess == nil || csrf == nil {
		t.Fatalf("login must set both cookies, got %v", resp.Cookies())
	}
	for _, c := range []*http.Cookie{sess, csrf} {
		if !c.Secure {
			t.Errorf("%s must be Secure in production", c.Name)
		}
		if c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
			t.Errorf("%s: want SameSite=Lax, Path=/, host-only; got %v %q %q", c.Name, c.SameSite, c.Path, c.Domain)
		}
	}
	if !sess.HttpOnly {
		t.Error("the session cookie must be HttpOnly")
	}
	if csrf.HttpOnly {
		t.Error("the CSRF cookie must be readable by the frontend (double-submit)")
	}
	// The response body must not carry the session token (nothing for JS to store).
	raw, _ := json.Marshal(resp.Header)
	if strings.Contains(string(raw), sess.Value) && !strings.Contains(strings.ToLower(string(raw)), "set-cookie") {
		t.Error("session token leaked outside Set-Cookie")
	}
}

func TestProductionCORSOnlyExactConfiguredOrigin(t *testing.T) {
	srv := newProdServer(t)
	for _, origin := range []string{"https://evil.example", "http://localhost:3000", "null", "*", "https://nodera.example.com.evil.example"} {
		req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/auth/login", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origin %q must not be allowed, got ACAO %q", origin, got)
		}
	}
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/auth/login", nil)
	req.Header.Set("Origin", "https://nodera.example.com")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://nodera.example.com" || resp.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("the configured origin must be reflected exactly with credentials: %v", resp.Header)
	}
}

func TestReadinessAndLivenessAreDistinct(t *testing.T) {
	srv := newProdServer(t)
	for path, want := range map[string]string{"/health": "ok", "/ready": "ready"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 || body["status"] != want {
			t.Errorf("%s: %d %v", path, resp.StatusCode, body)
		}
		if path == "/ready" {
			checks, _ := body["checks"].(map[string]any)
			if checks["database"] != "ok" || checks["redis"] != "not_configured" {
				t.Errorf("/ready checks = %v", checks)
			}
		}
	}
}

// A huge JSON body to a public endpoint must be refused, not buffered.
func TestOversizedJSONBodyIsRejected(t *testing.T) {
	srv := newProdServer(t)
	big := `{"email":"a@b.c","password":"` + strings.Repeat("x", 3<<20) + `"}`
	resp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("a 3 MiB login body must be refused, got %d", resp.StatusCode)
	}
}
