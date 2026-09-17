// Package logger provides Nodera's structured logging setup. Every log line
// is JSON so it can be correlated by request/correlation ID (rule 27).
package logger

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey struct{}

// New builds the root structured logger. Level defaults to info; set
// NODERA_LOG_LEVEL=debug for verbose output.
func New(env string) *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("NODERA_LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	l := slog.New(handler).With("service", "nodera-api", "env", env)
	return l
}

// WithContext attaches a logger (already annotated with request/correlation
// IDs) to a context so downstream code can retrieve it without threading it
// through every function signature.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the logger attached to ctx, or slog.Default() if none
// was attached (e.g. in a unit test that doesn't set one up).
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
