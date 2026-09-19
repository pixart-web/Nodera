package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/httpserver"
)

type ctxKeySessionToken struct{}
type ctxKeyUserID struct{}
type ctxKeyAuthContext struct{}

// ctxKeyCookieAuth marks a request as authenticated via the browser
// session cookie rather than an Authorization: Bearer header — the only
// signal requireCSRF (below) uses to decide whether CSRF verification
// applies, since a Bearer credential is never attached by the browser
// automatically and therefore carries no CSRF surface to begin with.
type ctxKeyCookieAuth struct{}

// requireSession resolves the caller identity from either an
// Authorization: Bearer <token> header (a session token from POST
// /auth/login, or an API token from POST /api-tokens — see docs/API.md)
// or, if no bearer header is present, the HttpOnly nodera_session cookie
// a browser sent automatically (docs/SECURITY.md "Browser
// authentication"). Bearer takes priority when both are present, so an
// API-token client sharing cookies from an unrelated browser session
// (unlikely, but not impossible for a tool built on top of a browser
// context) is never silently downgraded to a different identity. A
// session token/cookie resolves only a user ID here; the organization and
// permissions are resolved later by requireOrganization once X-Nodera-Org
// is known. An API token already carries its organization and permission
// scopes, so it resolves a full AuthContext immediately.
func (d apiDeps) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		viaCookie := false
		if token == "" {
			if cookie, err := r.Cookie(httpserver.SessionCookieName); err == nil && cookie.Value != "" {
				token = cookie.Value
				viaCookie = true
			}
		}
		if token == "" {
			httpserver.WriteError(w, r, apierr.Unauthenticated("missing bearer token"))
			return
		}

		if userID, err := d.identity.UserIDForSession(r.Context(), token); err == nil {
			if viaCookie && !d.requireCSRF(w, r) {
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeySessionToken{}, token)
			ctx = context.WithValue(ctx, ctxKeyUserID{}, userID)
			if viaCookie {
				ctx = context.WithValue(ctx, ctxKeyCookieAuth{}, true)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		} else if !errors.Is(err, identity.ErrSessionInvalid) {
			httpserver.WriteError(w, r, err)
			return
		} else if viaCookie {
			// A cookie value only ever carries a session token (never an
			// API token — see handleLogin), so a cookie that fails
			// session lookup is simply invalid/expired, not worth a
			// fallback API-token lookup below.
			httpserver.WriteError(w, r, apierr.Unauthenticated("session expired"))
			return
		}

		ac, err := d.identity.AuthContextForAPIToken(r.Context(), token, httpserver.RequestID(r))
		if err != nil {
			httpserver.WriteError(w, r, err)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyAuthContext{}, ac)
		ctx = context.WithValue(ctx, ctxKeyUserID{}, ac.ActorID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireCSRF enforces the double-submit CSRF check
// (httpserver.VerifyCSRF) for state-changing methods on a
// cookie-authenticated request. GET/HEAD/OPTIONS are exempt — the
// standard CSRF scoping, since those must not have side effects to begin
// with. Writes a normalized 403 and returns false if the check fails, so
// callers can `if !d.requireCSRF(w, r) { return }`.
func (d apiDeps) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if !httpserver.VerifyCSRF(r) {
		httpserver.WriteError(w, r, apierr.Forbidden("missing or invalid CSRF token"))
		return false
	}
	return true
}

// requireOrganization additionally resolves the X-Nodera-Org header into a
// full authctx.AuthContext (with resolved permissions), verifying the caller
// is actually a member. It must run after requireSession. If requireSession
// already resolved a full AuthContext (the API token path, which embeds its
// own organization), this only validates that an explicit X-Nodera-Org
// header, if present, agrees with it — it does not re-resolve anything.
func (d apiDeps) requireOrganization(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ac, ok := r.Context().Value(ctxKeyAuthContext{}).(authctx.AuthContext); ok {
			if orgHeader := r.Header.Get("X-Nodera-Org"); orgHeader != "" {
				parsed, err := uuid.Parse(orgHeader)
				if err != nil || parsed != ac.OrganizationID {
					httpserver.WriteError(w, r, apierr.Forbidden("X-Nodera-Org does not match this API token's organization"))
					return
				}
			}
			next.ServeHTTP(w, r)
			return
		}

		token, _ := r.Context().Value(ctxKeySessionToken{}).(string)
		orgHeader := r.Header.Get("X-Nodera-Org")
		orgID, err := uuid.Parse(orgHeader)
		if err != nil {
			httpserver.WriteError(w, r, apierr.Validation("missing or invalid X-Nodera-Org header"))
			return
		}

		ac, err := d.identity.AuthContextForSession(r.Context(), token, orgID, httpserver.RequestID(r))
		if err != nil {
			httpserver.WriteError(w, r, err)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyAuthContext{}, ac)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

// sessionTokenFromRequest returns the caller's session token from either
// the Authorization header or, failing that, the nodera_session cookie —
// the same resolution order requireSession uses. handleLogout needs this
// directly (rather than reading ctxKeySessionToken, which requireSession
// only ever populates for the session-token path, not the API-token
// path) since a cookie-authenticated browser session never sends an
// Authorization header at all.
func sessionTokenFromRequest(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		return t
	}
	if cookie, err := r.Cookie(httpserver.SessionCookieName); err == nil {
		return cookie.Value
	}
	return ""
}

func userIDFromRequest(r *http.Request) uuid.UUID {
	id, _ := r.Context().Value(ctxKeyUserID{}).(uuid.UUID)
	return id
}

// platformAuthContext builds the AuthContext platform-scoped endpoints
// (r.Get("/platform/...") — outside the requireOrganization group, since
// platform permissions apply across every organization) check against. An
// API-token caller already resolved a full AuthContext in requireSession;
// reuse it as-is (platformauth.Require only ever looks at ActorType/
// ActorID, never OrganizationID/Permissions, so an org-scoped API token
// works correctly here without granting it anything beyond what the
// underlying user actually holds). A session-token caller only resolved a
// bare user id (org selection happens later, if at all, and platform
// permissions don't need one), so build the minimal AuthContext directly.
func platformAuthContext(r *http.Request) authctx.AuthContext {
	if ac, ok := r.Context().Value(ctxKeyAuthContext{}).(authctx.AuthContext); ok {
		return ac
	}
	return authctx.AuthContext{ActorType: authctx.ActorUser, ActorID: userIDFromRequest(r)}
}

func mustAuthContext(r *http.Request) authctx.AuthContext {
	ac, _ := r.Context().Value(ctxKeyAuthContext{}).(authctx.AuthContext)
	return ac
}
