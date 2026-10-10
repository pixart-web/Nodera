// Package mock provides deterministic, in-memory implementations of every
// provider interface. They are used by tests and by DEMO mode. They never
// touch the host, the network, or any real infrastructure — and anything that
// uses them must say so (results are labelled provider="mock").
package mock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nodera/nodera/internal/providers"
)

// Faults lets a test make a named operation fail (once or always) so rollback
// and error paths are exercised for real.
type Faults struct {
	mu    sync.Mutex
	fails map[string]error
	once  map[string]bool
	Calls []string
}

func (f *Faults) FailOn(op string, err error)   { f.set(op, err, false) }
func (f *Faults) FailOnce(op string, err error) { f.set(op, err, true) }
func (f *Faults) Clear()                        { f.mu.Lock(); f.fails, f.once = nil, nil; f.mu.Unlock() }
func (f *Faults) set(op string, err error, once bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails == nil {
		f.fails, f.once = map[string]error{}, map[string]bool{}
	}
	f.fails[op], f.once[op] = err, once
}

// hit records a call and returns the injected error for op, if any.
func (f *Faults) hit(op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, op)
	if err, ok := f.fails[op]; ok {
		if f.once[op] {
			delete(f.fails, op)
		}
		return err
	}
	return nil
}

// CallCount returns how many times op was invoked.
func (f *Faults) CallCount(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.Calls {
		if c == op {
			n++
		}
	}
	return n
}

// NewSet returns a fully-populated provider set backed by mocks plus the
// handles tests use to inspect state and inject faults.
func NewSet() (providers.Set, *Handles) {
	faults := &Faults{}
	fs := NewFS(faults)
	h := &Handles{Faults: faults, FS: fs, Containers: NewContainers(faults), DB: NewDB(faults), DNS: NewDNS(faults), Certs: NewCerts(faults), Backups: NewBackups(faults), Monitoring: NewMonitoring(faults), Nodes: NewNodes(faults), Git: NewGit(faults)}
	return providers.Set{Containers: h.Containers, FS: h.FS, DB: h.DB, DNS: h.DNS, Certs: h.Certs, Backups: h.Backups, Monitoring: h.Monitoring, Nodes: h.Nodes, Git: h.Git}, h
}

type Handles struct {
	Faults     *Faults
	FS         *FS
	Containers *Containers
	DB         *DB
	DNS        *DNS
	Certs      *Certs
	Backups    *Backups
	Monitoring *Monitoring
	Nodes      *Nodes
	Git        *Git
}

// ---------------- Containers ----------------

type Containers struct {
	f  *Faults
	mu sync.Mutex
	M  map[string]providers.ContainerInfo
	S  map[string]providers.ContainerSpec
	L  map[string][]string
}

func NewContainers(f *Faults) *Containers {
	return &Containers{f: f, M: map[string]providers.ContainerInfo{}, S: map[string]providers.ContainerSpec{}, L: map[string][]string{}}
}

func (c *Containers) Create(_ context.Context, spec providers.ContainerSpec) (providers.ContainerInfo, bool, error) {
	if err := c.f.hit("container.create"); err != nil {
		return providers.ContainerInfo{}, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ex, ok := c.M[spec.Name]; ok {
		return ex, false, nil
	}
	info := providers.ContainerInfo{ID: "mock-" + spec.Name, Name: spec.Name, Image: spec.Image, State: "created", Labels: spec.Labels}
	c.M[spec.Name], c.S[spec.Name] = info, spec
	return info, true, nil
}
func (c *Containers) setState(name, state string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, ok := c.M[name]
	if !ok {
		return providers.ErrNotFound
	}
	info.State = state
	if state == "running" {
		now := time.Now()
		info.StartedAt = &now
	}
	c.M[name] = info
	c.L[name] = append(c.L[name], fmt.Sprintf("container %s -> %s", name, state))
	return nil
}
func (c *Containers) Start(_ context.Context, n string) error {
	if err := c.f.hit("container.start"); err != nil {
		return err
	}
	return c.setState(n, "running")
}
func (c *Containers) Stop(_ context.Context, n string) error {
	if err := c.f.hit("container.stop"); err != nil {
		return err
	}
	return c.setState(n, "stopped")
}
func (c *Containers) Restart(_ context.Context, n string) error {
	if err := c.f.hit("container.restart"); err != nil {
		return err
	}
	return c.setState(n, "running")
}
func (c *Containers) Remove(_ context.Context, n string) error {
	if err := c.f.hit("container.remove"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.M, n)
	delete(c.S, n)
	return nil // removing a missing container is not an error (idempotent)
}
func (c *Containers) Inspect(_ context.Context, n string) (providers.ContainerInfo, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i, ok := c.M[n]
	return i, ok, nil
}
func (c *Containers) List(_ context.Context, labels map[string]string) ([]providers.ContainerInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []providers.ContainerInfo
	for _, i := range c.M {
		match := true
		for k, v := range labels {
			if i.Labels[k] != v {
				match = false
			}
		}
		if match {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}
func (c *Containers) Logs(_ context.Context, n string, tail int) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.M[n]; !ok {
		return nil, providers.ErrNotFound
	}
	l := c.L[n]
	if tail > 0 && len(l) > tail {
		l = l[len(l)-tail:]
	}
	return append([]string(nil), l...), nil
}

// ---------------- Filesystem ----------------

type FS struct {
	f     *Faults
	mu    sync.Mutex
	Files map[string][]byte
	Dirs  map[string]bool
}

func NewFS(f *Faults) *FS {
	return &FS{f: f, Files: map[string][]byte{}, Dirs: map[string]bool{".": true}}
}

func clean(p string) (string, error) {
	if path.IsAbs(p) || strings.Contains(p, "..") {
		return "", fmt.Errorf("mock fs: path %q escapes the root", p)
	}
	return path.Clean(p), nil
}
func (m *FS) MkdirAll(_ context.Context, p string) error {
	if err := m.f.hit("fs.mkdir"); err != nil {
		return err
	}
	c, err := clean(p)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for d := c; d != "." && d != "/"; d = path.Dir(d) {
		m.Dirs[d] = true
	}
	return nil
}
func (m *FS) WriteFile(_ context.Context, p string, data []byte) error {
	if err := m.f.hit("fs.write"); err != nil {
		return err
	}
	c, err := clean(p)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Files[c] = append([]byte(nil), data...)
	return nil
}
func (m *FS) ReadFile(_ context.Context, p string) ([]byte, error) {
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.Files[c]
	if !ok {
		return nil, providers.ErrNotFound
	}
	return append([]byte(nil), d...), nil
}
func (m *FS) Exists(_ context.Context, p string) (bool, error) {
	c, err := clean(p)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, f := m.Files[c]
	return f || m.Dirs[c], nil
}
func (m *FS) Remove(_ context.Context, p string) error {
	if err := m.f.hit("fs.remove"); err != nil {
		return err
	}
	c, err := clean(p)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.Files {
		if k == c || strings.HasPrefix(k, c+"/") {
			delete(m.Files, k)
		}
	}
	for k := range m.Dirs {
		if k == c || strings.HasPrefix(k, c+"/") {
			delete(m.Dirs, k)
		}
	}
	return nil
}
func (m *FS) List(_ context.Context, p string) ([]providers.FileInfo, error) {
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []providers.FileInfo
	for k, v := range m.Files {
		if path.Dir(k) == c {
			out = append(out, providers.FileInfo{Path: k, Size: int64(len(v))})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out, nil
}
func (m *FS) DiskUsage(_ context.Context, p string) (int64, error) {
	c, err := clean(p)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for k, v := range m.Files {
		if k == c || strings.HasPrefix(k, c+"/") {
			n += int64(len(v))
		}
	}
	return n, nil
}
func (m *FS) AbsPath(p string) (string, error) { return clean(p) }

// ---------------- Database ----------------

type DB struct {
	f     *Faults
	mu    sync.Mutex
	Names map[string]string // name -> user
	Data  map[string][]byte
}

func NewDB(f *Faults) *DB { return &DB{f: f, Names: map[string]string{}, Data: map[string][]byte{}} }
func (d *DB) EnsureDatabase(_ context.Context, name, user, _ string) (bool, error) {
	if err := d.f.hit("db.ensure"); err != nil {
		return false, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.Names[name]; ok {
		return false, nil
	}
	d.Names[name] = user
	d.Data[name] = []byte("-- empty\n")
	return true, nil
}
func (d *DB) DropDatabase(_ context.Context, name, _ string) error {
	if err := d.f.hit("db.drop"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.Names, name)
	delete(d.Data, name)
	return nil
}
func (d *DB) Exists(_ context.Context, name string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.Names[name]
	return ok, nil
}
func (d *DB) Dump(_ context.Context, name string, w io.Writer) error {
	if err := d.f.hit("db.dump"); err != nil {
		return err
	}
	d.mu.Lock()
	data, ok := d.Data[name]
	d.mu.Unlock()
	if !ok {
		return providers.ErrNotFound
	}
	_, err := w.Write(data)
	return err
}
func (d *DB) Load(_ context.Context, name string, r io.Reader) error {
	if err := d.f.hit("db.load"); err != nil {
		return err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.Names[name]; !ok {
		return providers.ErrNotFound
	}
	d.Data[name] = b
	return nil
}

// ---------------- DNS ----------------

type DNS struct {
	f          *Faults
	mu         sync.Mutex
	Zones      map[string][]providers.DNSRecord
	Propagated bool // when false, CheckPropagation reports false (simulates TTL delay)
	seq        int
}

func NewDNS(f *Faults) *DNS {
	return &DNS{f: f, Zones: map[string][]providers.DNSRecord{}, Propagated: true}
}
func (d *DNS) EnsureZone(_ context.Context, domain string) error {
	if err := d.f.hit("dns.zone"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.Zones[domain]; !ok {
		d.Zones[domain] = nil
	}
	return nil
}
func (d *DNS) ListRecords(_ context.Context, domain string) ([]providers.DNSRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	z, ok := d.Zones[domain]
	if !ok {
		return nil, providers.ErrNotFound
	}
	return append([]providers.DNSRecord(nil), z...), nil
}
func (d *DNS) UpsertRecord(_ context.Context, domain string, rec providers.DNSRecord) (providers.DNSRecord, error) {
	if err := d.f.hit("dns.upsert"); err != nil {
		return providers.DNSRecord{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	z, ok := d.Zones[domain]
	if !ok {
		return providers.DNSRecord{}, providers.ErrNotFound
	}
	for i, r := range z {
		if r.Type == rec.Type && r.Name == rec.Name && r.Value == rec.Value {
			rec.ID = r.ID
			z[i] = rec
			return rec, nil
		}
	}
	d.seq++
	rec.ID = fmt.Sprintf("mock-rec-%d", d.seq)
	d.Zones[domain] = append(z, rec)
	return rec, nil
}
func (d *DNS) DeleteRecord(_ context.Context, domain, id string) error {
	if err := d.f.hit("dns.delete"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	z := d.Zones[domain]
	for i, r := range z {
		if r.ID == id {
			d.Zones[domain] = append(z[:i], z[i+1:]...)
			return nil
		}
	}
	return nil
}
func (d *DNS) CheckPropagation(_ context.Context, domain string, rec providers.DNSRecord) (bool, error) {
	if err := d.f.hit("dns.check"); err != nil {
		return false, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.Propagated {
		return false, nil
	}
	for _, r := range d.Zones[domain] {
		if r.Type == rec.Type && r.Name == rec.Name && r.Value == rec.Value {
			return true, nil
		}
	}
	return false, nil
}

// ---------------- Certificates ----------------

type Certs struct {
	f       *Faults
	mu      sync.Mutex
	Issued  int
	Revoked map[string]bool
	// Validity controls the issued certificate's lifetime (default 90 days).
	Validity time.Duration
}

func NewCerts(f *Faults) *Certs {
	return &Certs{f: f, Revoked: map[string]bool{}, Validity: 90 * 24 * time.Hour}
}
func (c *Certs) Name() string { return "mock" }
func (c *Certs) bundle(domains []string) providers.CertBundle {
	c.mu.Lock()
	c.Issued++
	n := c.Issued
	c.mu.Unlock()
	now := time.Now()
	// MOCK material — clearly not a real certificate.
	return providers.CertBundle{
		CertPEM: []byte("-----BEGIN MOCK CERTIFICATE-----\n" + strings.Join(domains, ",") + "\n-----END MOCK CERTIFICATE-----\n"),
		KeyPEM:  []byte(fmt.Sprintf("-----BEGIN MOCK PRIVATE KEY-----\nmock-%d\n-----END MOCK PRIVATE KEY-----\n", n)),
		Issuer:  "Nodera Mock CA", Serial: fmt.Sprintf("mock-%06d", n), NotBefore: now, NotAfter: now.Add(c.Validity),
	}
}
func (c *Certs) Issue(_ context.Context, r providers.IssueRequest) (providers.CertBundle, error) {
	if err := c.f.hit("cert.issue"); err != nil {
		return providers.CertBundle{}, err
	}
	return c.bundle(r.Domains), nil
}
func (c *Certs) Renew(_ context.Context, r providers.IssueRequest) (providers.CertBundle, error) {
	if err := c.f.hit("cert.renew"); err != nil {
		return providers.CertBundle{}, err
	}
	return c.bundle(r.Domains), nil
}
func (c *Certs) Revoke(_ context.Context, serial string) error {
	if err := c.f.hit("cert.revoke"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Revoked[serial] = true
	return nil
}
func (c *Certs) Inspect(_ context.Context, pem []byte) (providers.CertInfo, error) {
	if !bytes.Contains(pem, []byte("MOCK CERTIFICATE")) {
		return providers.CertInfo{}, errors.New("mock: not a mock certificate")
	}
	return providers.CertInfo{Issuer: "Nodera Mock CA"}, nil
}

// ---------------- Backups ----------------

type Backups struct {
	f     *Faults
	mu    sync.Mutex
	Store map[string][]byte
	// Corrupt makes Verify fail for a ref, simulating bit rot.
	Corrupt map[string]bool
}

func NewBackups(f *Faults) *Backups {
	return &Backups{f: f, Store: map[string][]byte{}, Corrupt: map[string]bool{}}
}
func (b *Backups) Create(ctx context.Context, id string, src providers.BackupSource) (providers.BackupArtifact, error) {
	if err := b.f.hit("backup.create"); err != nil {
		return providers.BackupArtifact{}, err
	}
	var buf bytes.Buffer
	buf.WriteString("MOCKBACKUP:" + id + "\n")
	for _, p := range src.Paths {
		buf.WriteString("path:" + p + "\n")
	}
	if src.DatabaseDump != nil {
		buf.WriteString("db:\n")
		if err := src.DatabaseDump(ctx, &buf); err != nil {
			return providers.BackupArtifact{}, err
		}
	}
	sum := sha256.Sum256(buf.Bytes())
	ref := "mock://" + id
	b.mu.Lock()
	b.Store[ref] = buf.Bytes()
	b.mu.Unlock()
	return providers.BackupArtifact{Ref: ref, Size: int64(buf.Len()), SHA256: hex.EncodeToString(sum[:])}, nil
}
func (b *Backups) Verify(_ context.Context, ref, want string) error {
	if err := b.f.hit("backup.verify"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.Store[ref]
	if !ok {
		return providers.ErrNotFound
	}
	sum := sha256.Sum256(data)
	if b.Corrupt[ref] || hex.EncodeToString(sum[:]) != want {
		return errors.New("checksum mismatch")
	}
	return nil
}
func (b *Backups) Restore(ctx context.Context, ref string, _ providers.FilesystemProvider, dbLoad func(context.Context, io.Reader) error) error {
	if err := b.f.hit("backup.restore"); err != nil {
		return err
	}
	b.mu.Lock()
	data, ok := b.Store[ref]
	b.mu.Unlock()
	if !ok {
		return providers.ErrNotFound
	}
	if i := bytes.Index(data, []byte("db:\n")); i >= 0 && dbLoad != nil {
		return dbLoad(ctx, bytes.NewReader(data[i+4:]))
	}
	return nil
}
func (b *Backups) Delete(_ context.Context, ref string) error {
	if err := b.f.hit("backup.delete"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.Store, ref)
	return nil
}

// ---------------- Monitoring ----------------

type Monitoring struct {
	f       *Faults
	mu      sync.Mutex
	Down    map[string]bool // targets reported as failing
	SSLDays map[string]int
	Metrics providers.NodeMetrics
}

func NewMonitoring(f *Faults) *Monitoring {
	return &Monitoring{f: f, Down: map[string]bool{}, SSLDays: map[string]int{}, Metrics: providers.NodeMetrics{CPUPercent: 12, RAMUsedMB: 2457, RAMTotalMB: 8192, DiskUsedGB: 46, DiskTotalGB: 200, NetUpMbps: 12, NetDownMbps: 8}}
}
func (m *Monitoring) SetDown(target string, down bool) {
	m.mu.Lock()
	m.Down[target] = down
	m.mu.Unlock()
}
func (m *Monitoring) check(op, target string) (providers.CheckResult, error) {
	if err := m.f.hit(op); err != nil {
		return providers.CheckResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Down[target] {
		return providers.CheckResult{OK: false, Detail: "mock: target marked down"}, nil
	}
	return providers.CheckResult{OK: true, LatencyMs: 12, Detail: "mock"}, nil
}
func (m *Monitoring) CheckHTTP(_ context.Context, u string, _ int) (providers.CheckResult, error) {
	return m.check("monitor.http", u)
}
func (m *Monitoring) CheckTCP(_ context.Context, a string) (providers.CheckResult, error) {
	return m.check("monitor.tcp", a)
}
func (m *Monitoring) CheckDNS(_ context.Context, h string) (providers.CheckResult, error) {
	return m.check("monitor.dns", h)
}
func (m *Monitoring) CheckSSL(_ context.Context, h string) (providers.CheckResult, error) {
	r, err := m.check("monitor.ssl", h)
	if err != nil || !r.OK {
		return r, err
	}
	m.mu.Lock()
	days, ok := m.SSLDays[h]
	m.mu.Unlock()
	if !ok {
		days = 60
	}
	r.Extra = map[string]any{"days_remaining": days}
	return r, nil
}
func (m *Monitoring) CollectNodeMetrics(_ context.Context, _ string) (providers.NodeMetrics, error) {
	if err := m.f.hit("monitor.metrics"); err != nil {
		return providers.NodeMetrics{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Metrics, nil
}

// ---------------- Nodes ----------------

type Nodes struct{ f *Faults }

func NewNodes(f *Faults) *Nodes { return &Nodes{f: f} }
func (n *Nodes) Describe(_ context.Context, ref string) (providers.NodeInfo, error) {
	return providers.NodeInfo{Ref: ref, Hostname: ref, Provider: "mock", Status: "running"}, nil
}
func (n *Nodes) Reboot(_ context.Context, _ string) error { return n.f.hit("node.reboot") }

// ---------------- Git ----------------

type Git struct {
	f     *Faults
	Repos map[string]map[string][]byte // repo -> file path -> content (the "checkout")
}

func NewGit(f *Faults) *Git {
	return &Git{f: f, Repos: map[string]map[string][]byte{"demo/site": {"index.php": []byte("<?php echo 'hello';"), "VERSION": []byte("1.0.0")}}}
}
func (g *Git) ListRepositories(context.Context) ([]providers.Repository, error) {
	var out []providers.Repository
	for k := range g.Repos {
		out = append(out, providers.Repository{FullName: k, DefaultBranch: "main"})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].FullName < out[b].FullName })
	return out, nil
}
func (g *Git) ListBranches(_ context.Context, repo string) ([]string, error) {
	if _, ok := g.Repos[repo]; !ok {
		return nil, providers.ErrNotFound
	}
	return []string{"main"}, nil
}
func (g *Git) ListCommits(_ context.Context, repo, _ string, _ int) ([]providers.Commit, error) {
	if _, ok := g.Repos[repo]; !ok {
		return nil, providers.ErrNotFound
	}
	return []providers.Commit{{SHA: "mockc0ffee", Message: "mock commit", Author: "mock", At: time.Now()}}, nil
}
func (g *Git) Fetch(ctx context.Context, repo, ref string, dest providers.FilesystemProvider, destPath string) (string, error) {
	if err := g.f.hit("git.fetch"); err != nil {
		return "", err
	}
	files, ok := g.Repos[repo]
	if !ok {
		return "", providers.ErrNotFound
	}
	for p, data := range files {
		if err := dest.WriteFile(ctx, path.Join(destPath, p), data); err != nil {
			return "", err
		}
	}
	return "mockc0ffee", nil
}
func (g *Git) CreateWebhook(_ context.Context, repo, _ string) (string, error) {
	return "mock-hook-" + repo, nil
}
