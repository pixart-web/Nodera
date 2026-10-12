package main

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
)

// trustedProxies are the networks whose X-Forwarded-For header is believed. It is
// empty by default, which means "behind no proxy": RemoteAddr is the client.
//
// Behind Traefik the TCP peer is the proxy for EVERY user, so without this all
// IP-keyed limits (login 5/5min, signup 3/h) would be one shared bucket: one
// attacker could lock everybody out, and a real brute force would hide among them.
var trustedProxies atomic.Pointer[[]netip.Prefix]

// setTrustedProxies parses "10.0.0.0/8,172.16.0.0/12,203.0.113.7" (bare addresses
// become /32 or /128).
func setTrustedProxies(list []string) error {
	var out []netip.Prefix
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return &net.ParseError{Type: "trusted proxy address or CIDR", Text: item}
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	trustedProxies.Store(&out)
	return nil
}

func isTrusted(a netip.Addr) bool {
	p := trustedProxies.Load()
	if p == nil {
		return false
	}
	a = a.Unmap()
	for _, pre := range *p {
		if pre.Contains(a) {
			return true
		}
	}
	return false
}

// clientIP is the address rate limits and audit use for the caller.
//
// When the TCP peer is a trusted proxy, X-Forwarded-For is walked from the RIGHT
// (the entries a trusted proxy appended) and the first address that is not itself
// a trusted proxy is the client. Entries to the left of it are attacker-controlled
// and ignored, so sending a forged X-Forwarded-For through the proxy cannot choose
// the identity that is rate-limited. If the peer is not trusted the header is ignored.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !isTrusted(peer) {
		return host
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return host // malformed entry: do not guess, fall back to the peer
		}
		if !isTrusted(a) {
			return a.Unmap().String()
		}
	}
	return host
}
