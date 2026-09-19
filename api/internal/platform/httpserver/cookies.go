package httpserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"
)

// SessionCookieName / CSRFCookieName are the two cookies a human browser
// session uses (see docs/SECURITY.md "Browser authentication"):
// SessionCookieName is HttpOnly — JavaScript on the frontend origin can
// never read it, closing the session-token-theft-via-XSS surface a
// localStorage-stored bearer token has. CSRFCookieName deliberately is
// NOT HttpOnly: the frontend must read it and echo it back as a header
// (the double-submit pattern — see RequireCSRF/VerifyCSRF below), which
// only same-origin JavaScript can do.
const (
	SessionCookieName = "nodera_session"
	CSRFCookieName    = "nodera_csrf"
	CSRFHeaderName    = "X-CSRF-Token"
)

// NewCSRFToken generates a fresh random CSRF token — 32 bytes of entropy,
// same size class as a session token (ADR-005).
func NewCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SetSessionCookie sets the HttpOnly session cookie. secure must be true
// in production (HTTPS-only transport) — see config.Config.Env; a
// development server over plain HTTP cannot set a browser-honored Secure
// cookie, so this is a deliberate, documented dev-mode exception (rule
// 23), not a silently-degraded production default.
func SetSessionCookie(w http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		// Lax, not Strict: session cookies must still be sent on the
		// frontend's own top-level navigations and same-site fetch/XHR
		// calls (the API and the web app share a registrable domain in
		// the intended deployment — see docs/SECURITY.md), which Lax
		// permits; only actual cross-site requests are withheld. Strict
		// would also withhold the cookie on a plain link/redirect into
		// the app from outside it, which Lax correctly allows since it's
		// a top-level GET navigation, not a state change.
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie expires the session cookie immediately (logout).
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// SetCSRFCookie sets the double-submit CSRF cookie and returns the token
// value so the caller can also (optionally) return it in the response
// body for a client that prefers reading it there over parsing
// document.cookie.
func SetCSRFCookie(w http.ResponseWriter, ttl time.Duration, secure bool) (string, error) {
	token, err := NewCSRFToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: false, // frontend JS must read this to echo it back
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}

// ClearCSRFCookie expires the CSRF cookie (logout).
func ClearCSRFCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// VerifyCSRF implements the double-submit cookie check: the value the
// CSRF cookie carries must match the value the caller supplied in the
// X-CSRF-Token header. An attacker's cross-site page can trigger the
// browser to attach nodera_session/nodera_csrf automatically to a forged
// request, but same-origin policy prevents it from ever reading the CSRF
// cookie's value itself (document.cookie is scoped per-origin) — so it
// cannot produce a matching header, and the request is rejected. This is
// checked only for cookie-authenticated requests (see
// cmd/server/middleware.go's requireSession): a Bearer-token caller
// supplies its credential in a header the browser never attaches
// automatically, so it carries no CSRF surface to begin with.
func VerifyCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get(CSRFHeaderName)
	if header == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) == 1
}
