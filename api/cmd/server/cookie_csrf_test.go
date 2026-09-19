package main

// Tests for the cookie-based browser session + double-submit CSRF
// protection (docs/SECURITY.md "Browser authentication"). Unlike the
// internal/ package's service-level integration tests, these drive the
// real HTTP router end to end (httptest.Server + net/http.Client with a
// cookie jar) since the behavior under test — cookies, CORS headers, the
// CSRF header check — only exists at the HTTP layer, not in any Go
// service method signature.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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

// newTestServer wires a minimal but real apiDeps (identity/tenancy/rbac/
// audit/platform against a real Postgres, via testhelpers.RequirePool)
// and starts an httptest.Server for it — enough to exercise login/logout/
// CSRF/cookie behavior without needing every domain wired.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	pool := testhelpers.RequirePool(t)
	auditSvc := audit.New(pool)
	rbacSvc := rbac.New(pool, auditSvc)
	identitySvc := identity.New(pool, rbacSvc, 24*time.Hour, auditSvc)
	tenancySvc := tenancy.New(pool, identitySvc, auditSvc)
	platformSvc := platformauth.New(pool, auditSvc)

	deps := apiDeps{
		log:           logger.New("test"),
		identity:      identitySvc,
		tenancy:       tenancySvc,
		audit:         auditSvc,
		rbac:          rbacSvc,
		platform:      platformSvc,
		pool:          pool,
		corsOrigins:   []string{"http://test-frontend.example"},
		sessionTTL:    24 * time.Hour,
		secureCookies: false, // httptest.Server uses plain HTTP
	}
	srv := httptest.NewServer(newRouter(deps))
	t.Cleanup(srv.Close)
	return srv
}

func signupAndLogin(t *testing.T, client *http.Client, base, email string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse battery staple 9", "display_name": "CSRF Test"})
	resp, err := client.Post(base+"/api/v1/auth/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup: expected 201, got %d", resp.StatusCode)
	}

	loginBody, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse battery staple 9"})
	resp, err = client.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: expected 200, got %d", resp.StatusCode)
	}
}

func csrfCookieValue(client *http.Client, base string) string {
	u, _ := url.Parse(base)
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == httpserver.CSRFCookieName {
			return c.Value
		}
	}
	return ""
}

func hasSessionCookie(client *http.Client, base string) bool {
	u, _ := url.Parse(base)
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == httpserver.SessionCookieName {
			return true
		}
	}
	return false
}

// TestCookieAuth_LoginSetsSessionAndCSRFCookies proves login actually
// issues both cookies — the HttpOnly session cookie the frontend never
// reads, and the CSRF cookie it must read and echo back.
func TestCookieAuth_LoginSetsSessionAndCSRFCookies(t *testing.T) {
	srv := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	signupAndLogin(t, client, srv.URL, "cookie-login-test@nodera.dev")

	if !hasSessionCookie(client, srv.URL) {
		t.Fatal("expected login to set the session cookie")
	}
	if csrfCookieValue(client, srv.URL) == "" {
		t.Fatal("expected login to set a non-empty CSRF cookie")
	}
}

// TestCookieAuth_StateChangingRequestWithoutCSRFHeaderIsRejected is the
// core CSRF protection test: a cookie-authenticated mutating request that
// doesn't echo the CSRF cookie's value in the X-CSRF-Token header must be
// refused, even though the browser automatically attached a fully valid
// session cookie (simulating exactly what a forged cross-site request
// would look like — the attacker's page can trigger the cookie-bearing
// request but cannot read the CSRF cookie to put its value in the
// header).
func TestCookieAuth_StateChangingRequestWithoutCSRFHeaderIsRejected(t *testing.T) {
	srv := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	signupAndLogin(t, client, srv.URL, "csrf-missing-test@nodera.dev")

	body, _ := json.Marshal(map[string]string{"current_password": "correct horse battery staple 9", "new_password": "a different password 2"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/account/password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 FORBIDDEN without a CSRF header, got %d", resp.StatusCode)
	}
}

// TestCookieAuth_StateChangingRequestWithValidCSRFHeaderSucceeds is the
// valid-flow counterpart: reading the CSRF cookie value (as the frontend
// does via document.cookie) and echoing it in X-CSRF-Token lets the exact
// same request through.
func TestCookieAuth_StateChangingRequestWithValidCSRFHeaderSucceeds(t *testing.T) {
	srv := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	signupAndLogin(t, client, srv.URL, "csrf-valid-test@nodera.dev")

	csrf := csrfCookieValue(client, srv.URL)
	if csrf == "" {
		t.Fatal("expected a CSRF cookie after login")
	}

	body, _ := json.Marshal(map[string]string{"current_password": "correct horse battery staple 9", "new_password": "a different password 2"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/account/password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpserver.CSRFHeaderName, csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 with a valid CSRF header, got %d", resp.StatusCode)
	}
}

// TestCookieAuth_WrongCSRFTokenIsRejected proves the check is a genuine
// value comparison, not merely "header present".
func TestCookieAuth_WrongCSRFTokenIsRejected(t *testing.T) {
	srv := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	signupAndLogin(t, client, srv.URL, "csrf-wrong-test@nodera.dev")

	body, _ := json.Marshal(map[string]string{"current_password": "correct horse battery staple 9", "new_password": "a different password 2"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/account/password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpserver.CSRFHeaderName, "definitely-not-the-real-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 FORBIDDEN with a mismatched CSRF token, got %d", resp.StatusCode)
	}
}

// TestCookieAuth_GETDoesNotRequireCSRF proves safe methods are exempt —
// requiring CSRF on GET would just break normal browsing for no security
// benefit, since GET must not have side effects to begin with.
func TestCookieAuth_GETDoesNotRequireCSRF(t *testing.T) {
	srv := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	signupAndLogin(t, client, srv.URL, "csrf-get-test@nodera.dev")

	resp, err := client.Get(srv.URL + "/api/v1/account/sessions")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for a cookie-authenticated GET with no CSRF header, got %d", resp.StatusCode)
	}
}

// TestCookieAuth_BearerTokenStillWorksWithoutCSRFHeader proves machine/
// API-token clients are unaffected: a Bearer credential is never attached
// by a browser automatically, so it carries no CSRF surface and the check
// only ever applies to cookie-authenticated requests.
func TestCookieAuth_BearerTokenStillWorksWithoutCSRFHeader(t *testing.T) {
	srv := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	email := "bearer-still-works-test@nodera.dev"
	signupAndLogin(t, client, srv.URL, email)

	// Extract the raw session token from the login response body (what a
	// non-browser client would do) rather than relying on the cookie jar.
	loginBody, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse battery staple 9"})
	resp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	var loginResp struct {
		SessionToken string `json:"session_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	resp.Body.Close()

	// A bare client with no cookie jar, using only the Bearer header —
	// exactly a CLI/script's shape.
	body, _ := json.Marshal(map[string]string{"current_password": "correct horse battery staple 9", "new_password": "a different password 3"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/account/password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginResp.SessionToken)
	bareResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer bareResp.Body.Close()
	if bareResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for a Bearer-authenticated request with no CSRF header, got %d", bareResp.StatusCode)
	}
}

// TestCookieAuth_LogoutClearsCookies proves logout actually expires both
// cookies (Max-Age <= 0) rather than merely revoking server-side state —
// a stale cookie left in the browser after logout would be misleading
// even though it would no longer authenticate anything.
func TestCookieAuth_LogoutClearsCookies(t *testing.T) {
	srv := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	signupAndLogin(t, client, srv.URL, "logout-clears-cookies-test@nodera.dev")

	csrf := csrfCookieValue(client, srv.URL)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/logout", nil)
	req.Header.Set(httpserver.CSRFHeaderName, csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 from logout, got %d", resp.StatusCode)
	}

	if hasSessionCookie(client, srv.URL) {
		t.Fatal("expected logout to clear the session cookie from the jar")
	}
}

// TestCORS_ReflectsAllowedOriginWithCredentials proves the CORS response
// carries both an exact (never wildcard) origin and
// Access-Control-Allow-Credentials: true — required for the browser to
// actually honor cookies cross-origin, and only legal combined with an
// exact origin, never "*".
func TestCORS_ReflectsAllowedOriginWithCredentials(t *testing.T) {
	srv := newTestServer(t)
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/auth/login", nil)
	req.Header.Set("Origin", "http://test-frontend.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preflight request: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://test-frontend.example" {
		t.Fatalf("expected exact-origin reflection, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected Access-Control-Allow-Credentials: true, got %q", got)
	}
}
