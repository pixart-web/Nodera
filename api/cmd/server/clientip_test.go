package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func req(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func TestClientIPWithoutTrustedProxiesIgnoresForwardedFor(t *testing.T) {
	if err := setTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	if got := clientIP(req("203.0.113.9:5555", "1.2.3.4")); got != "203.0.113.9" {
		t.Fatalf("an untrusted peer cannot choose its identity: %s", got)
	}
}

func TestClientIPBehindTraefik(t *testing.T) {
	if err := setTrustedProxies([]string{"172.16.0.0/12", "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = setTrustedProxies(nil) })
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"real client appended by the proxy", "172.18.0.5:4000", []string{"198.51.100.20"}, "198.51.100.20"},
		{"forged leftmost entry is ignored", "172.18.0.5:4000", []string{"6.6.6.6, 198.51.100.20"}, "198.51.100.20"},
		{"forged entry in a separate header line", "172.18.0.5:4000", []string{"6.6.6.6", "198.51.100.20"}, "198.51.100.20"},
		{"two trusted hops", "172.18.0.5:4000", []string{"198.51.100.20, 10.1.2.3"}, "198.51.100.20"},
		{"ipv6 client", "172.18.0.5:4000", []string{"2001:db8::7"}, "2001:db8::7"},
		{"peer is not a proxy: header ignored", "203.0.113.9:1", []string{"198.51.100.20"}, "203.0.113.9"},
		{"no header: the proxy itself", "172.18.0.5:4000", nil, "172.18.0.5"},
		{"garbage entry falls back to the peer", "172.18.0.5:4000", []string{"not-an-ip"}, "172.18.0.5"},
	}
	for _, c := range cases {
		if got := clientIP(req(c.remote, c.xff...)); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestSetTrustedProxiesRejectsGarbage(t *testing.T) {
	t.Cleanup(func() { _ = setTrustedProxies(nil) })
	if err := setTrustedProxies([]string{"10.0.0.0/8", "bananas"}); err == nil {
		t.Fatal("garbage must be rejected at startup")
	}
	if err := setTrustedProxies([]string{"203.0.113.7", " 10.0.0.0/8 ", ""}); err != nil {
		t.Fatal(err)
	}
}
