// Package handlers holds Tool Gateway handler implementations that are
// self-contained enough not to belong inside cmd/server/main.go (which
// composes existing domain services per ADR-002) or a domain package of
// their own. CheckSSL is the first: a genuine TLS handshake against a real
// domain, not a fabricated result (rule 36) — and it needs no credential or
// external service dependency to be real, unlike most of the seeded tool
// catalog.
package handlers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/netpolicy"
)

const dialTimeout = 10 * time.Second

// CheckSSL connects to resourceID (a "host" or "host:port" — port defaults
// to 443) and reports the leaf certificate's validity window, days
// remaining, and whether it verifies against the system trust store. It
// performs a real TLS handshake every call; there is no caching, so
// repeated calls against the same host cost a real connection each time —
// acceptable for an interactively-invoked tool, not meant for polling at
// scale (a monitoring/collector job would be the right shape for that,
// tracked in docs/ROADMAP.md).
//
// check_ssl is reachable by any organization member holding
// infrastructure.read (a 'read' risk tool — see migration 0011) and takes
// an arbitrary caller-supplied hostname, making it exactly the
// SSRF primitive docs/SECURITY.md "Outbound network / SSRF policy"
// describes: without validation, a caller could point it at an internal
// service, a cloud metadata endpoint, or the control plane's own loopback
// interface and use the certificate-inspection response as an oracle for
// what's reachable on the private network. This is the production
// handler, always PublicOnly — see checkSSLWithPolicy for the
// policy-injectable version checkssl_test.go uses to exercise the genuine
// TLS-handshake path against a local httptest server without weakening
// what actually ships.
func CheckSSL(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error) {
	return checkSSLWithPolicy(ctx, netpolicy.Policy{Level: netpolicy.PublicOnly}, resourceID)
}

// The handshake itself skips Go's built-in verification
// (InsecureSkipVerify) so a self-signed, expired, or hostname-mismatched
// certificate can still be inspected and reported on — that's the whole
// point of a diagnostic tool — but trust is then checked explicitly
// afterward via Certificate.Verify against the system root pool, so
// "trusted" in the result is a real, independently-computed answer, not
// papered over by skipping verification.
func checkSSLWithPolicy(ctx context.Context, policy netpolicy.Policy, resourceID string) (any, error) {
	if resourceID == "" {
		return nil, apierr.Validation("resource_id must be a hostname (optionally host:port)")
	}

	target, err := netpolicy.Resolve(ctx, policy, resourceID, "443")
	if err != nil {
		var blocked *netpolicy.BlockedError
		if errors.As(err, &blocked) {
			return nil, apierr.Forbidden(blocked.Error())
		}
		return nil, apierr.Validation(err.Error())
	}
	host := target.Host

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	// Dial the already-validated IP directly (never target.Host again) —
	// this is what closes the DNS-rebinding gap: re-resolving the
	// hostname here could hand back a different, unvalidated address.
	// ServerName stays the original hostname so TLS SNI and the
	// certificate's hostname verification below are still correct.
	dialer := tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}} //nolint:gosec // verified explicitly below
	rawConn, err := dialer.DialContext(dialCtx, "tcp", target.DialAddr())
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, fmt.Sprintf("TLS handshake with %s failed", target.DialAddr()), err)
	}
	conn := rawConn.(*tls.Conn)
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, apierr.New(apierr.CodeUnavailable, "server presented no certificate")
	}
	leaf := certs[0]

	now := time.Now()
	daysRemaining := int(leaf.NotAfter.Sub(now).Hours() / 24)

	trusted := true
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host}); err != nil {
		trusted = false
	}

	return map[string]any{
		"host":           host,
		"subject":        leaf.Subject.CommonName,
		"issuer":         leaf.Issuer.CommonName,
		"not_before":     leaf.NotBefore,
		"not_after":      leaf.NotAfter,
		"days_remaining": daysRemaining,
		"expired":        now.After(leaf.NotAfter),
		"trusted":        trusted,
		"dns_names":      leaf.DNSNames,
	}, nil
}
