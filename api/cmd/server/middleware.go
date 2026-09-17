package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/httpserver"
)

type ctxKeySessionToken struct{}
type ctxKeyUserID struct{}
type ctxKeyAuthContext struct{}

// requireSession resolves the Authorization: Bearer <token> header to a
// user ID, without yet requiring an organization — used by routes like
// "list my organizations" that a user can call before picking one.
func (d apiDeps) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			httpserver.WriteError(w, r, apierr.Unauthenticated("missing bearer token"))
			return
		}

		userID, err := d.resolveSessionUserID(r.Context(), token)
		if err != nil {
			httpserver.WriteError(w, r, err)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeySessionToken{}, token)
		ctx = context.WithValue(ctx, ctxKeyUserID{}, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireOrganization additionally resolves the X-Nodera-Org header into a
// full authctx.AuthContext (with resolved permissions), verifying the caller
// is actually a member. It must run after requireSession.
func (d apiDeps) requireOrganization(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// resolveSessionUserID is a thin wrapper so requireSession doesn't need to
// know about identity's internal session/token hashing — it goes through
// AuthContextForSession's underlying lookup indirectly via a zero-org probe
// is wrong for permissions, so instead identity exposes this directly.
func (d apiDeps) resolveSessionUserID(ctx context.Context, token string) (uuid.UUID, error) {
	return d.identity.UserIDForSession(ctx, token)
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

func userIDFromRequest(r *http.Request) uuid.UUID {
	id, _ := r.Context().Value(ctxKeyUserID{}).(uuid.UUID)
	return id
}

func mustAuthContext(r *http.Request) authctx.AuthContext {
	ac, _ := r.Context().Value(ctxKeyAuthContext{}).(authctx.AuthContext)
	return ac
}
