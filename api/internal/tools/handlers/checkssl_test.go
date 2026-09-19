// package handlers (white-box, not handlers_test) so these tests can call
// checkSSLWithPolicy directly — needed to exercise the genuine
// TLS-handshake path against a local httptest server (necessarily bound
// to 127.0.0.1, which the real CheckSSL/PublicOnly policy correctly
// refuses — see TestCheckSSL_BlocksLoopbackTarget) without weakening the
// production default.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/netpolicy"
)

// httptest.NewTLSServer gives us a real TLS listener with a real
// (self-signed) certificate — enough to prove the handshake logic
// performs a genuine handshake and reads real certificate fields, without
// any network dependency or fabricated data (rule 36). It runs against
// InternalAllowed since httptest necessarily binds to loopback, which the
// production CheckSSL entry point (always PublicOnly) would correctly
// refuse — see TestCheckSSL_BlocksLoopbackTarget for that path.
func TestCheckSSL_Success(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")

	result, err := checkSSLWithPolicy(context.Background(), netpolicy.Policy{Level: netpolicy.InternalAllowed}, host)
	if err != nil {
		t.Fatalf("checkSSLWithPolicy: %v", err)
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

// An unreachable *public* address (TEST-NET-1, RFC 5737 — reserved for
// documentation/examples, guaranteed never to have anything listening)
// should fail the connection itself, not the SSRF policy — proving these
// are two independently-testable failure modes, not one conflated check.
func TestCheckSSL_UnreachableHost(t *testing.T) {
	_, err := CheckSSL(context.Background(), authctx.AuthContext{}, "domain", "192.0.2.1:1", nil)
	if err == nil {
		t.Fatal("expected an error connecting to a host with nothing listening")
	}
}

func TestCheckSSL_EmptyResourceID(t *testing.T) {
	_, err := CheckSSL(context.Background(), authctx.AuthContext{}, "domain", "", nil)
	if err == nil {
		t.Fatal("expected an error for an empty resource_id")
	}
}

func TestCheckSSL_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := CheckSSL(ctx, authctx.AuthContext{}, "domain", "example.com:443", nil)
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
}

// --- SSRF policy (docs/SECURITY.md "Outbound network / SSRF policy") ---

// The production CheckSSL entry point (always PublicOnly) must refuse a
// loopback target outright — never even attempt the connection — which is
// exactly what a caller trying to probe the control plane's own local
// services via check_ssl would supply.
func TestCheckSSL_BlocksLoopbackTarget(t *testing.T) {
	_, err := CheckSSL(context.Background(), authctx.AuthContext{}, "domain", "127.0.0.1:443", nil)
	if err == nil {
		t.Fatal("expected CheckSSL to refuse a loopback target")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// IPv6 loopback (::1) must be refused too — proving the policy isn't an
// IPv4-only check that a caller could bypass with an IPv6 literal.
func TestCheckSSL_BlocksIPv6Loopback(t *testing.T) {
	_, err := CheckSSL(context.Background(), authctx.AuthContext{}, "domain", "[::1]:443", nil)
	if err == nil {
		t.Fatal("expected CheckSSL to refuse an IPv6 loopback target")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// The cloud metadata endpoint every major provider uses
// (169.254.169.254) is link-local, and must be refused the same way any
// other link-local address is — with no separate metadata-specific
// special case to accidentally miss.
func TestCheckSSL_BlocksCloudMetadataAddress(t *testing.T) {
	_, err := CheckSSL(context.Background(), authctx.AuthContext{}, "domain", "169.254.169.254:443", nil)
	if err == nil {
		t.Fatal("expected CheckSSL to refuse the cloud metadata address")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// A private RFC1918 address must be refused.
func TestCheckSSL_BlocksPrivateAddress(t *testing.T) {
	_, err := CheckSSL(context.Background(), authctx.AuthContext{}, "domain", "10.0.0.5:443", nil)
	if err == nil {
		t.Fatal("expected CheckSSL to refuse a private RFC1918 target")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// A legitimate public IPv6 literal must NOT be blocked — proving the
// policy doesn't accidentally reject arbitrary IPv6 addresses wholesale
// (the failure mode of a naive "any address containing ':' looks weird,
// block it" implementation). The connection itself will fail (nothing is
// listening on port 1), but that must be a connection error, not a
// policy rejection — this is what actually distinguishes "blocked" from
// "unreachable" for this address family.
func TestCheckSSL_DoesNotBlockPublicIPv6(t *testing.T) {
	// 2606:4700:4700::1111 is Cloudflare's public DNS resolver — a real,
	// stable public IPv6 address, used here only as an IP literal (no DNS
	// lookup occurs), so this test has no network dependency for the
	// policy check itself.
	_, err := CheckSSL(context.Background(), authctx.AuthContext{}, "domain", "[2606:4700:4700::1111]:1", nil)
	if err == nil {
		t.Fatal("expected a connection error (nothing listens on port 1)")
	}
	if ae, ok := err.(*apierr.Error); ok && ae.Code == apierr.CodeForbidden {
		t.Fatalf("expected a connection failure, not a policy rejection, for a public IPv6 address: %v", err)
	}
}
