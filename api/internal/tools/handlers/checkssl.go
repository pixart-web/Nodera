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
	"fmt"
	"net"
	"time"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
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
// The handshake itself skips Go's built-in verification
// (InsecureSkipVerify) so a self-signed, expired, or hostname-mismatched
// certificate can still be inspected and reported on — that's the whole
// point of a diagnostic tool — but trust is then checked explicitly
// afterward via Certificate.Verify against the system root pool, so
// "trusted" in the result is a real, independently-computed answer, not
// papered over by skipping verification.
func CheckSSL(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error) {
	if resourceID == "" {
		return nil, apierr.Validation("resource_id must be a hostname (optionally host:port)")
	}

	host, port, err := net.SplitHostPort(resourceID)
	if err != nil {
		host, port = resourceID, "443"
	}
	addr := net.JoinHostPort(host, port)

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	dialer := tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}} //nolint:gosec // verified explicitly below
	rawConn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, fmt.Sprintf("TLS handshake with %s failed", addr), err)
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
