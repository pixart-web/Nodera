package local

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"syscall"
	"time"

	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers"
)

// Monitoring performs REAL probes. Every outbound target is validated by
// internal/platform/netpolicy first (SSRF protection); Policy defaults to
// PublicOnly and is relaxed to InternalAllowed only by explicit configuration
// (the Nodera control plane legitimately probes its own infrastructure).
type Monitoring struct {
	Policy   netpolicy.Policy
	DiskPath string
	Timeout  time.Duration
}

func NewMonitoring(policy netpolicy.Policy) *Monitoring {
	return &Monitoring{Policy: policy, DiskPath: "/", Timeout: 10 * time.Second}
}

func (m *Monitoring) resolve(ctx context.Context, hostport, defPort string) (netpolicy.ResolvedTarget, error) {
	return netpolicy.Resolve(ctx, m.Policy, hostport, defPort)
}

func (m *Monitoring) CheckHTTP(ctx context.Context, rawURL string, expect int) (providers.CheckResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return providers.CheckResult{}, fmt.Errorf("invalid http(s) url")
	}
	port := "80"
	if u.Scheme == "https" {
		port = "443"
	}
	target, err := m.resolve(ctx, u.Host, port)
	if err != nil {
		return providers.CheckResult{OK: false, Detail: err.Error()}, nil
	}
	client := &http.Client{
		Timeout: m.Timeout,
		// Dial the validated IP only (no second DNS lookup) and never follow
		// redirects to unvalidated hosts.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: m.Timeout}).DialContext(ctx, network, target.DialAddr())
			},
			TLSClientConfig: &tls.Config{ServerName: target.Host},
		},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	start := time.Now()
	resp, err := client.Do(req)
	lat := int(time.Since(start).Milliseconds())
	if err != nil {
		return providers.CheckResult{OK: false, LatencyMs: lat, Detail: err.Error()}, nil
	}
	defer resp.Body.Close()
	ok := resp.StatusCode == expect || (expect == 0 && resp.StatusCode < 400)
	return providers.CheckResult{OK: ok, LatencyMs: lat, Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}, nil
}

func (m *Monitoring) CheckTCP(ctx context.Context, addr string) (providers.CheckResult, error) {
	target, err := m.resolve(ctx, addr, "")
	if err != nil {
		return providers.CheckResult{OK: false, Detail: err.Error()}, nil
	}
	start := time.Now()
	conn, err := (&net.Dialer{Timeout: m.Timeout}).DialContext(ctx, "tcp", target.DialAddr())
	lat := int(time.Since(start).Milliseconds())
	if err != nil {
		return providers.CheckResult{OK: false, LatencyMs: lat, Detail: err.Error()}, nil
	}
	conn.Close()
	return providers.CheckResult{OK: true, LatencyMs: lat, Detail: "connected"}, nil
}

func (m *Monitoring) CheckDNS(ctx context.Context, host string) (providers.CheckResult, error) {
	start := time.Now()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	lat := int(time.Since(start).Milliseconds())
	if err != nil || len(addrs) == 0 {
		return providers.CheckResult{OK: false, LatencyMs: lat, Detail: "no resolution"}, nil
	}
	return providers.CheckResult{OK: true, LatencyMs: lat, Detail: addrs[0], Extra: map[string]any{"addresses": addrs}}, nil
}

func (m *Monitoring) CheckSSL(ctx context.Context, host string) (providers.CheckResult, error) {
	target, err := m.resolve(ctx, host, "443")
	if err != nil {
		return providers.CheckResult{OK: false, Detail: err.Error()}, nil
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: m.Timeout}, Config: &tls.Config{ServerName: target.Host, InsecureSkipVerify: true}} //nolint:gosec // inspection only
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", target.DialAddr())
	lat := int(time.Since(start).Milliseconds())
	if err != nil {
		return providers.CheckResult{OK: false, LatencyMs: lat, Detail: err.Error()}, nil
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return providers.CheckResult{OK: false, LatencyMs: lat, Detail: "no certificate"}, nil
	}
	days := int(time.Until(certs[0].NotAfter).Hours() / 24)
	return providers.CheckResult{OK: days > 0, LatencyMs: lat, Detail: certs[0].Issuer.CommonName, Extra: map[string]any{"days_remaining": days}}, nil
}

// CollectNodeMetrics reports what the Go runtime and OS can tell us portably
// (disk usage via statfs, memory via runtime). CPU/network need the Node Agent
// on the real node; here they are reported as 0 rather than invented.
func (m *Monitoring) CollectNodeMetrics(context.Context, string) (providers.NodeMetrics, error) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out := providers.NodeMetrics{RAMUsedMB: float64(ms.Sys) / 1048576}
	var st syscall.Statfs_t
	if err := syscall.Statfs(m.DiskPath, &st); err == nil {
		total := float64(st.Blocks) * float64(st.Bsize)
		free := float64(st.Bavail) * float64(st.Bsize)
		out.DiskTotalGB = total / 1e9
		out.DiskUsedGB = (total - free) / 1e9
	}
	return out, nil
}
