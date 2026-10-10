package network

import (
	"net"
	"regexp"
	"strings"

	"github.com/nodera/nodera/internal/platform/apierr"
)

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeDomain lowercases and validates a registrable/host domain name.
func NormalizeDomain(in string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(in), ".")))
	if d == "" || len(d) > 253 {
		return "", apierr.Validation("domain name is required (max 253 characters)")
	}
	if net.ParseIP(d) != nil {
		return "", apierr.Validation("an IP address is not a domain name")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return "", apierr.Validation("domain must have at least two labels, e.g. example.com")
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return "", apierr.Validation("domain contains an invalid label: " + l)
		}
	}
	return d, nil
}

type RecordInput struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority"`
}

var recNameRe = regexp.MustCompile(`^(@|\*|[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?(\.[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?)*)$`)

// Validate normalises and checks a DNS record (type-specific value rules).
func (in *RecordInput) Validate() error {
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Value = strings.TrimSpace(in.Value)
	if in.Name == "" {
		in.Name = "@"
	}
	if !recNameRe.MatchString(in.Name) {
		return apierr.Validation("invalid record name")
	}
	if in.TTL == 0 {
		in.TTL = 300
	}
	if in.TTL < 30 || in.TTL > 86400 {
		return apierr.Validation("ttl must be between 30 and 86400 seconds")
	}
	if in.Value == "" || len(in.Value) > 2048 {
		return apierr.Validation("value is required")
	}
	switch in.Type {
	case "A":
		ip := net.ParseIP(in.Value)
		if ip == nil || ip.To4() == nil {
			return apierr.Validation("A record value must be an IPv4 address")
		}
	case "AAAA":
		ip := net.ParseIP(in.Value)
		if ip == nil || ip.To4() != nil {
			return apierr.Validation("AAAA record value must be an IPv6 address")
		}
	case "CNAME":
		if in.Name == "@" {
			return apierr.Validation("a CNAME cannot be set at the zone apex")
		}
		h := strings.TrimSuffix(strings.ToLower(in.Value), ".")
		if net.ParseIP(h) != nil || !hostRe.MatchString(h) {
			return apierr.Validation("CNAME value must be a hostname")
		}
	case "MX":
		h := strings.TrimSuffix(strings.ToLower(in.Value), ".")
		if !hostRe.MatchString(h) {
			return apierr.Validation("MX value must be a hostname")
		}
		if in.Priority < 0 || in.Priority > 65535 {
			return apierr.Validation("MX priority must be 0-65535")
		}
	case "TXT":
		if len(in.Value) > 1024 || strings.ContainsAny(in.Value, "\r\n\x00") {
			return apierr.Validation("TXT value is too long or contains control characters")
		}
	case "CAA":
		parts := strings.Fields(in.Value)
		if len(parts) != 3 || (parts[1] != "issue" && parts[1] != "issuewild" && parts[1] != "iodef") {
			return apierr.Validation(`CAA value must look like: 0 issue "letsencrypt.org"`)
		}
	default:
		return apierr.Validation("unsupported record type (A, AAAA, CNAME, MX, TXT, CAA)")
	}
	return nil
}

var hostRe = regexp.MustCompile(`^([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?\.)+[a-z]{2,63}$|^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
