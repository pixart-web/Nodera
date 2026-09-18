package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/tools/handlers"
)

// httptest.NewTLSServer gives us a real TLS listener with a real
// (self-signed) certificate — enough to prove CheckSSL performs a genuine
// handshake and reads real certificate fields, without any network
// dependency or fabricated data (rule 36).
func TestCheckSSL_Success(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")

	result, err := handlers.CheckSSL(context.Background(), authctx.AuthContext{}, "domain", host, nil)
	if err != nil {
		t.Fatalf("CheckSSL: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected a map result, got %T", result)
	}
	if m["expired"] != false {
		t.Errorf("expected the test server's freshly-issued cert to not be expired, got %v", m["expired"])
	}
	if m["trusted"] != false {
		t.Errorf("expected httptest's self-signed cert to be reported untrusted, got %v", m["trusted"])
	}
	if _, ok := m["days_remaining"].(int); !ok {
		t.Errorf("expected days_remaining to be an int, got %T", m["days_remaining"])
	}
	if m["subject"] == nil {
		t.Errorf("expected a non-nil subject")
	}
}

func TestCheckSSL_UnreachableHost(t *testing.T) {
	// Port 1 is reserved/unlikely to have anything listening; connecting
	// should fail fast rather than hang or fabricate a result.
	_, err := handlers.CheckSSL(context.Background(), authctx.AuthContext{}, "domain", "127.0.0.1:1", nil)
	if err == nil {
		t.Fatal("expected an error connecting to a host with nothing listening")
	}
}

func TestCheckSSL_EmptyResourceID(t *testing.T) {
	_, err := handlers.CheckSSL(context.Background(), authctx.AuthContext{}, "domain", "", nil)
	if err == nil {
		t.Fatal("expected an error for an empty resource_id")
	}
}

func TestCheckSSL_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := handlers.CheckSSL(ctx, authctx.AuthContext{}, "domain", "example.com:443", nil)
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
}
