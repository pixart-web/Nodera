package netpolicy

import (
	"context"
	"net"
	"testing"
)

// TestClassify_BlocksExpectedAddressClasses covers every class PublicOnly
// must reject, IPv4 and IPv6, including the cloud metadata address (which
// is blocked purely by being link-local — see classify's doc comment, no
// separate metadata-specific case exists to accidentally miss).
func TestClassify_BlocksExpectedAddressClasses(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"IPv4 loopback", "127.0.0.1"},
		{"IPv4 loopback range", "127.10.20.30"},
		{"IPv6 loopback", "::1"},
		{"IPv4 unspecified", "0.0.0.0"},
		{"IPv6 unspecified", "::"},
		{"IPv4 link-local", "169.254.1.1"},
		{"cloud metadata (AWS/GCP/Azure/DO)", "169.254.169.254"},
		{"IPv6 link-local", "fe80::1"},
		{"RFC1918 10/8", "10.0.0.1"},
		{"RFC1918 172.16/12", "172.16.5.5"},
		{"RFC1918 192.168/16", "192.168.1.1"},
		{"IPv6 unique local (RFC4193)", "fc00::1"},
		{"IPv4 multicast", "224.0.0.1"},
		{"IPv6 multicast", "ff02::1"},
		{"IPv4-mapped IPv6 loopback", "::ffff:127.0.0.1"},
		{"IPv4-mapped IPv6 private", "::ffff:10.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("test bug: %q did not parse as an IP", tc.ip)
			}
			blocked, reason := classify(ip)
			if !blocked {
				t.Fatalf("expected %s (%s) to be blocked", tc.name, tc.ip)
			}
			if reason == "" {
				t.Fatalf("expected a non-empty reason for blocking %s", tc.name)
			}
		})
	}
}

// TestClassify_AllowsPublicAddresses proves the policy isn't
// over-broad — real public addresses, both IPv4 and IPv6, must not be
// blocked.
func TestClassify_AllowsPublicAddresses(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"public IPv4 (documentation range doesn't matter for classify)", "93.184.216.34"},
		{"public IPv6 (Cloudflare DNS)", "2606:4700:4700::1111"},
		{"public IPv6 (Google DNS)", "2001:4860:4860::8888"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("test bug: %q did not parse as an IP", tc.ip)
			}
			if blocked, reason := classify(ip); blocked {
				t.Fatalf("expected %s (%s) to be allowed, got blocked: %s", tc.name, tc.ip, reason)
			}
		})
	}
}

// TestResolve_PublicOnlyBlocksLiteralPrivateIP proves the end-to-end
// Resolve path (not just classify) refuses a private IP literal under
// PublicOnly, with no network access needed since an IP literal skips DNS.
func TestResolve_PublicOnlyBlocksLiteralPrivateIP(t *testing.T) {
	_, err := Resolve(context.Background(), Policy{Level: PublicOnly}, "10.0.0.1:443", "443")
	if err == nil {
		t.Fatal("expected a private IP literal to be blocked under PublicOnly")
	}
	var blocked *BlockedError
	if !isBlockedError(err, &blocked) {
		t.Fatalf("expected a *BlockedError, got %T: %v", err, err)
	}
}

// TestResolve_PublicOnlyAllowsLiteralPublicIP proves a public IP literal
// passes and DialAddr reflects it exactly, with the port defaulted
// correctly when none is supplied.
func TestResolve_PublicOnlyAllowsLiteralPublicIP(t *testing.T) {
	target, err := Resolve(context.Background(), Policy{Level: PublicOnly}, "93.184.216.34", "443")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target.DialAddr() != "93.184.216.34:443" {
		t.Fatalf("unexpected dial address: %s", target.DialAddr())
	}
	if target.Host != "93.184.216.34" {
		t.Fatalf("unexpected host: %s", target.Host)
	}
}

// TestResolve_InternalAllowedPermitsPrivateAddress proves the graduated
// policy actually works — InternalAllowed is the explicit, opt-in
// exception, not a dead code path.
func TestResolve_InternalAllowedPermitsPrivateAddress(t *testing.T) {
	target, err := Resolve(context.Background(), Policy{Level: InternalAllowed}, "10.0.0.1:8080", "443")
	if err != nil {
		t.Fatalf("expected InternalAllowed to permit a private address: %v", err)
	}
	if target.DialAddr() != "10.0.0.1:8080" {
		t.Fatalf("unexpected dial address: %s", target.DialAddr())
	}
}

// TestResolve_RegisteredResourcesConsultsCallback proves the middle
// policy tier only allows a private address the caller's own callback
// explicitly recognizes — not every private address, and not none.
func TestResolve_RegisteredResourcesConsultsCallback(t *testing.T) {
	allowed := net.ParseIP("10.0.0.1")
	policy := Policy{
		Level: RegisteredResources,
		AllowedResource: func(ip net.IP) bool {
			return ip.Equal(allowed)
		},
	}

	if _, err := Resolve(context.Background(), policy, "10.0.0.1:443", "443"); err != nil {
		t.Fatalf("expected the registered resource to be permitted: %v", err)
	}
	if _, err := Resolve(context.Background(), policy, "10.0.0.2:443", "443"); err == nil {
		t.Fatal("expected an unregistered private address to still be blocked")
	}
}

// TestResolve_RegisteredResourcesWithNilCallbackBehavesLikePublicOnly
// proves a nil callback fails closed rather than silently permitting
// everything.
func TestResolve_RegisteredResourcesWithNilCallbackBehavesLikePublicOnly(t *testing.T) {
	policy := Policy{Level: RegisteredResources, AllowedResource: nil}
	if _, err := Resolve(context.Background(), policy, "10.0.0.1:443", "443"); err == nil {
		t.Fatal("expected a nil AllowedResource callback to block a private address, not permit it")
	}
}

// TestResolve_RejectsEmptyHost / InvalidPort cover basic input validation.
func TestResolve_RejectsEmptyHost(t *testing.T) {
	if _, err := Resolve(context.Background(), Policy{Level: PublicOnly}, "", "443"); err == nil {
		t.Fatal("expected an empty host to be rejected")
	}
}

func TestResolve_RejectsInvalidPort(t *testing.T) {
	if _, err := Resolve(context.Background(), Policy{Level: PublicOnly}, "example.com:not-a-port", "443"); err == nil {
		t.Fatal("expected an invalid port to be rejected")
	}
}

func isBlockedError(err error, target **BlockedError) bool {
	be, ok := err.(*BlockedError)
	if ok {
		*target = be
	}
	return ok
}
