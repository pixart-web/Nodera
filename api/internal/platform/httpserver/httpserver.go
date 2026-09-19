// Package httpserver provides the cross-cutting HTTP plumbing shared by
// every route: request/correlation IDs, structured request logging, panic
// recovery, and normalized JSON responses (rule 22, rule 27, rule 35).
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/logger"
)

type ctxKeyRequestID struct{}

// RequestID returns the request/correlation ID for this request, generating
// one if the client did not supply an X-Request-ID header. It is used both
// for log correlation and as the audit log's correlation_id.
func RequestID(r *http.Request) string {
	if v := r.Context().Value(ctxKeyRequestID{}); v != nil {
		return v.(string)
	}
	return ""
}

// WithRequestID is middleware that ensures every request has a correlation
// ID, propagates it to the response headers, and attaches a request-scoped
// logger carrying it.
func WithRequestID(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" {
				id = uuid.NewString()
			}
			w.Header().Set("X-Request-ID", id)
			ctx := context.WithValue(r.Context(), ctxKeyRequestID{}, id)
			ctx = logger.WithContext(ctx, base.With("request_id", id))
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

// Recover is middleware that turns a panic in a handler into a normalized
// 500 response instead of crashing the process or leaking a stack trace to
// the client (rule 35).
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.FromContext(r.Context()).Error("panic recovered", "panic", rec)
				WriteError(w, r, apierr.New(apierr.CodeInternal, "internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORS returns middleware that permits cross-origin browser requests from
// an allow-listed set of origins (the web/ frontend). It reflects a
// matching origin back (never "*" — required both because Authorization
// headers are in use and because Access-Control-Allow-Credentials: true
// is never legal combined with a wildcard origin per the Fetch spec) and
// answers preflight OPTIONS requests directly. Access-Control-Allow-
// Credentials is required so the browser will actually send/accept the
// HttpOnly session cookie (docs/SECURITY.md "Browser authentication")
// cross-origin; it has no effect on Bearer-token callers, who don't rely
// on cookies at all. An empty allow-list permits nothing cross-origin —
// same-origin and non-browser callers are unaffected either way, since
// CORS is enforced by the browser, not this server (rule 28).
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if _, ok := allowed[origin]; ok {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Vary", "Origin")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Nodera-Org, X-Request-ID, "+CSRFHeaderName)
				h.Set("Access-Control-Max-Age", "600")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets response headers appropriate for a JSON API (rule
// 28). There is no HTML response from this server, so no CSP/XSS-specific
// header is needed — these guard against MIME-sniffing, clickjacking of any
// embedded response, and caching of what may be sensitive JSON.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Logging is middleware that logs one structured line per request.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.FromContext(r.Context()).Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", RequestID(r),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// WriteJSON writes v as a JSON response body with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorResponse struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id,omitempty"`
	} `json:"error"`
}

// StatusFor maps a normalized apierr.Code to an HTTP status code.
func StatusFor(code apierr.Code) int {
	switch code {
	case apierr.CodeValidation:
		return http.StatusBadRequest
	case apierr.CodeUnauthenticated:
		return http.StatusUnauthorized
	case apierr.CodeForbidden:
		return http.StatusForbidden
	case apierr.CodeNotFound:
		return http.StatusNotFound
	case apierr.CodeConflict:
		return http.StatusConflict
	case apierr.CodeRateLimited:
		return http.StatusTooManyRequests
	case apierr.CodeNotImplemented:
		return http.StatusNotImplemented
	case apierr.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// WriteError writes err as a normalized JSON error response. Non-*apierr.Error
// values are treated as internal errors and their detail is logged but never
// sent to the client (rule 35: never leak raw driver/vendor errors).
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		logger.FromContext(r.Context()).Error("unhandled internal error", "error", err, "request_id", RequestID(r))
		ae = apierr.New(apierr.CodeInternal, "internal error")
	}
	if ae.Code == apierr.CodeInternal {
		logger.FromContext(r.Context()).Error("internal error", "error", ae.Err, "request_id", RequestID(r))
	}

	resp := errorResponse{}
	resp.Error.Code = string(ae.Code)
	resp.Error.Message = ae.Message
	resp.Error.RequestID = RequestID(r)
	WriteJSON(w, StatusFor(ae.Code), resp)
}
