// Package netpolicy is Nodera's reusable outbound-target validation
// component (docs/SECURITY.md "Outbound network / SSRF policy"). Any tool
// or handler that connects to a caller-supplied hostname (check_ssl today,
// more diagnostic tools later) is a network-probing/SSRF primitive unless
// its target is validated first — a caller could otherwise point it at an
// internal service, a cloud metadata endpoint, or the control plane's own
// loopback interface. This package exists so that validation is written
// once, correctly, and reused, rather than reimplemented ad hoc in every
// future tool.
package netpolicy

import (
	"context"
	"fmt"
	"net"
	"strconv"
)

// Level is a policy's overall stance on which address classes a resolved
// target may fall into. Nodera will eventually need to legitimately reach
// registered infrastructure nodes that live on private networks, so this
// is a graduated policy, not a single "private IPs are always forbidden"
// hack.
type Level int

const (
	// PublicOnly rejects loopback, unspecified, link-local, and private
	// (RFC1918 / RFC4193) addresses outright — the correct default for a
	// diagnostic tool an ordinary organization member can point at an
	// arbitrary hostname (check_ssl). No exceptions.
	PublicOnly Level = iota
	// RegisteredResources additionally allows an address the policy's
	// AllowedResource callback recognizes as one of Nodera's own
	// registered resources (e.g. a node in the infrastructure inventory)
	// even if it falls in a private range — for a future tool that
	// legitimately needs to reach infrastructure Nodera itself manages.
	// Not used by any tool yet (the Node Agent this would serve doesn't
	// exist — rule 27); the level exists now so adding that tool later
	// doesn't require redesigning this package.
	RegisteredResources
	// InternalAllowed permits every address class, including private and
	// loopback. Intended only for a platform-admin-configured integration
	// that is explicitly meant to reach internal infrastructure (see
	// docs/SECURITY.md "AI provider network safety" for the analogous
	// platform-admin-trusted case) — never the default for anything an
	// ordinary organization member can trigger.
	InternalAllowed
)

// Policy configures one Resolve call.
type Policy struct {
	Level Level
	// AllowedResource is consulted only when Level == RegisteredResources,
	// and reports whether ip is a resource Nodera itself has registered.
	// A nil func with RegisteredResources behaves exactly like PublicOnly
	// (no registered resources are considered allowed) rather than
	// panicking or silently allowing everything.
	AllowedResource func(ip net.IP) bool
}

// ResolvedTarget is a validated, ready-to-dial address. Callers must dial
// IP.String() (via DialAddr), never re-resolve Host at connection time —
// doing so would reopen exactly the DNS-rebinding gap this package exists
// to close (the name could resolve to a different, unvalidated address
// between the check and the connection). Host is kept only for TLS SNI /
// an HTTP Host header, where the original hostname is still needed.
type ResolvedTarget struct {
	Host string
	IP   net.IP
	Port string
}

// DialAddr returns the "ip:port" string to actually connect to.
func (t ResolvedTarget) DialAddr() string {
	return net.JoinHostPort(t.IP.String(), t.Port)
}

// BlockedError explains why a target was refused, safe to surface to the
// caller (it never echoes internal network topology beyond the address
// class name).
type BlockedError struct {
	Host   string
	IP     net.IP
	Reason string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("target %s (%s) is not permitted: %s", e.Host, e.IP, e.Reason)
}

// Resolve validates hostport ("host" or "host:port", port defaults to
// defaultPort) against policy and returns a ResolvedTarget bound to one
// specific, already-validated IP address. hostport may be a hostname (DNS
// is resolved exactly once, here) or an IP literal.
//
// DNS rebinding: because the returned ResolvedTarget carries a resolved
// IP rather than a hostname, and callers are required to dial that IP
// directly (ResolvedTarget.DialAddr), there is no window between
// validation and connection in which re-resolving the hostname could
// hand the caller a different, unvalidated address — the classic
// TOCTOU/rebinding attack against hostname-based allow-listing.
func Resolve(ctx context.Context, policy Policy, hostport, defaultPort string) (ResolvedTarget, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, defaultPort
	}
	if host == "" {
		return ResolvedTarget{}, fmt.Errorf("netpolicy: empty host")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ResolvedTarget{}, fmt.Errorf("netpolicy: invalid port %q", port)
	}

	var ips []net.IP
	if literal := net.ParseIP(host); literal != nil {
		ips = []net.IP{literal}
	} else {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return ResolvedTarget{}, fmt.Errorf("netpolicy: failed to resolve %q: %w", host, err)
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	if len(ips) == 0 {
		return ResolvedTarget{}, fmt.Errorf("netpolicy: %q did not resolve to any address", host)
	}

	var lastReason string
	for _, ip := range ips {
		if blocked, reason := classify(ip); blocked {
			if policy.Level == InternalAllowed {
				return ResolvedTarget{Host: host, IP: ip, Port: port}, nil
			}
			if policy.Level == RegisteredResources && policy.AllowedResource != nil && policy.AllowedResource(ip) {
				return ResolvedTarget{Host: host, IP: ip, Port: port}, nil
			}
			lastReason = reason
			continue
		}
		return ResolvedTarget{Host: host, IP: ip, Port: port}, nil
	}
	return ResolvedTarget{}, &BlockedError{Host: host, IP: ips[0], Reason: lastReason}
}

// classify reports whether ip falls into an address class PublicOnly
// rejects, and why. Go's net.IP methods already correctly unwrap
// IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1) before classifying,
// so no separate normalization step is needed here.
func classify(ip net.IP) (blocked bool, reason string) {
	switch {
	case ip.IsLoopback():
		return true, "loopback address"
	case ip.IsUnspecified():
		return true, "unspecified address"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// Covers 169.254.0.0/16 and fe80::/10 — which is also where the
		// cloud metadata endpoint 169.254.169.254 (AWS/GCP/Azure/
		// DigitalOcean all use this address) and its IPv6 equivalents
		// live, so no separate metadata-address special case is needed:
		// it is unreachable under PublicOnly purely by virtue of being
		// link-local.
		return true, "link-local address"
	case ip.IsPrivate():
		// Go's IsPrivate covers RFC1918 (10/8, 172.16/12, 192.168/16) and
		// RFC4193 IPv6 unique local addresses (fc00::/7).
		return true, "private address"
	case ip.IsMulticast():
		return true, "multicast address"
	default:
		return false, ""
	}
}
