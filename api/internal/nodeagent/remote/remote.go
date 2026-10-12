// Package remote implements provider interfaces by dispatching signed,
// allowlisted commands to a Node Agent and waiting for the result. Engines
// use it transparently: swapping the local/mock provider for a remote one is
// how an operation targets a real node.
package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/nodeagent"
	"github.com/nodera/nodera/internal/nodeagent/protocol"
	"github.com/nodera/nodera/internal/providers"
)

type client struct {
	svc       *nodeagent.Service
	orgID     uuid.UUID
	agentID   uuid.UUID
	jobID     *uuid.UUID
	requestID string
	timeout   time.Duration
}

// NewSet returns a provider set backed by one agent. jobID/requestID tie the
// commands to the originating operation for observability.
func NewSet(svc *nodeagent.Service, orgID, agentID uuid.UUID, jobID *uuid.UUID, requestID string) providers.Set {
	c := &client{svc: svc, orgID: orgID, agentID: agentID, jobID: jobID, requestID: requestID, timeout: 2 * time.Minute}
	return providers.Set{Containers: (*containers)(c), FS: (*fsys)(c), Monitoring: (*monitoring)(c)}
}

func (c *client) call(ctx context.Context, op string, params any, out any) error {
	res, err := c.svc.Dispatch(ctx, c.orgID, c.agentID, c.jobID, c.requestID, op, params, c.timeout)
	if err != nil {
		return err
	}
	if !res.OK {
		return errors.New(res.Error)
	}
	if out != nil && len(res.Result) > 0 {
		return json.Unmarshal(res.Result, out)
	}
	return nil
}

type containers client

func (c *containers) cl() *client { return (*client)(c) }
func (c *containers) Create(ctx context.Context, spec providers.ContainerSpec) (providers.ContainerInfo, bool, error) {
	var out struct {
		Container providers.ContainerInfo `json:"container"`
		Created   bool                    `json:"created"`
	}
	err := c.cl().call(ctx, "docker.create", spec, &out)
	return out.Container, out.Created, err
}
func (c *containers) simple(ctx context.Context, op, name string) error {
	return c.cl().call(ctx, op, map[string]string{"name": name}, nil)
}
func (c *containers) Start(ctx context.Context, n string) error {
	return c.simple(ctx, "docker.start", n)
}
func (c *containers) Stop(ctx context.Context, n string) error {
	return c.simple(ctx, "docker.stop", n)
}
func (c *containers) Restart(ctx context.Context, n string) error {
	return c.simple(ctx, "docker.restart", n)
}
func (c *containers) Remove(ctx context.Context, n string) error {
	return c.simple(ctx, "docker.remove", n)
}
func (c *containers) Inspect(ctx context.Context, n string) (providers.ContainerInfo, bool, error) {
	var out struct {
		Container providers.ContainerInfo `json:"container"`
		Found     bool                    `json:"found"`
	}
	err := c.cl().call(ctx, "docker.inspect", map[string]string{"name": n}, &out)
	return out.Container, out.Found, err
}
func (c *containers) List(context.Context, map[string]string) ([]providers.ContainerInfo, error) {
	return nil, errors.New("listing containers is not part of the agent allowlist")
}
func (c *containers) Logs(ctx context.Context, n string, tail int) ([]string, error) {
	var out struct {
		Lines []string `json:"lines"`
	}
	err := c.cl().call(ctx, "docker.logs", map[string]any{"name": n, "tail": tail}, &out)
	return out.Lines, err
}

type fsys client

func (f *fsys) cl() *client { return (*client)(f) }
func (f *fsys) MkdirAll(ctx context.Context, p string) error {
	return f.cl().call(ctx, "filesystem.mkdir", map[string]string{"path": p}, nil)
}
func (f *fsys) WriteFile(ctx context.Context, p string, data []byte) error {
	return f.cl().call(ctx, "filesystem.write", map[string]string{"path": p, "content_b64": base64.StdEncoding.EncodeToString(data)}, nil)
}
func (f *fsys) ReadFile(ctx context.Context, p string) ([]byte, error) {
	var out struct {
		C string `json:"content_b64"`
	}
	if err := f.cl().call(ctx, "filesystem.read", map[string]string{"path": p}, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.C)
}
func (f *fsys) Exists(ctx context.Context, p string) (bool, error) {
	_, err := f.ReadFile(ctx, p)
	if err != nil {
		return false, nil
	}
	return true, nil
}
func (f *fsys) Remove(ctx context.Context, p string) error {
	return f.cl().call(ctx, "filesystem.remove", map[string]string{"path": p}, nil)
}
func (f *fsys) List(context.Context, string) ([]providers.FileInfo, error) {
	return nil, errors.New("listing is not part of the agent allowlist")
}
func (f *fsys) DiskUsage(context.Context, string) (int64, error) {
	return 0, errors.New("disk usage is not part of the agent allowlist")
}
func (f *fsys) AbsPath(p string) (string, error) { return p, protocol.SafeRelPath(p) }

type monitoring client

func (m *monitoring) cl() *client { return (*client)(m) }
func (m *monitoring) health(ctx context.Context, kind, target string, expect int) (providers.CheckResult, error) {
	var out providers.CheckResult
	err := m.cl().call(ctx, "service.health", map[string]any{"kind": kind, "target": target, "expect_status": expect}, &out)
	return out, err
}
func (m *monitoring) CheckHTTP(ctx context.Context, u string, e int) (providers.CheckResult, error) {
	return m.health(ctx, "http", u, e)
}
func (m *monitoring) CheckTCP(ctx context.Context, a string) (providers.CheckResult, error) {
	return m.health(ctx, "tcp", a, 0)
}
func (m *monitoring) CheckDNS(context.Context, string) (providers.CheckResult, error) {
	return providers.CheckResult{}, fmt.Errorf("dns checks run from the control plane, not the agent")
}
func (m *monitoring) CheckSSL(context.Context, string) (providers.CheckResult, error) {
	return providers.CheckResult{}, fmt.Errorf("ssl checks run from the control plane, not the agent")
}
func (m *monitoring) CollectNodeMetrics(ctx context.Context, _ string) (providers.NodeMetrics, error) {
	var out providers.NodeMetrics
	err := m.cl().call(ctx, "metrics.collect", map[string]string{}, &out)
	return out, err
}

// ---- Local node (ProviderMode "agent") ----

type nodeClient struct {
	svc     *nodeagent.Service
	agentID uuid.UUID
	timeout time.Duration
	mu      sync.Mutex
	org     uuid.UUID // cached after the first successful lookup
}

func (c *nodeClient) orgID(ctx context.Context) (uuid.UUID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.org == uuid.Nil {
		org, err := c.svc.OrgOf(ctx, c.agentID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("local node agent is not available: %w", err)
		}
		c.org = org
	}
	return c.org, nil
}

func (c *nodeClient) call(ctx context.Context, op string, params any, out any) error {
	org, err := c.orgID(ctx)
	if err != nil {
		return err
	}
	res, err := c.svc.Dispatch(ctx, org, c.agentID, nil, "", op, params, c.timeout)
	if err != nil {
		return err
	}
	if !res.OK {
		return errors.New(res.Error)
	}
	if out != nil && len(res.Result) > 0 {
		return json.Unmarshal(res.Result, out)
	}
	return nil
}

// LocalNode returns Containers and Database providers whose every call is a
// signed, allowlisted command to the one local agent. The API process needs no
// Docker socket. local is the API's own filesystem provider; it must be the
// same directory the agent uses (shared host path), because database dumps and
// loads move through files there instead of through the command channel.
func LocalNode(svc *nodeagent.Service, agentID uuid.UUID, local providers.FilesystemProvider) (providers.ContainerProvider, providers.DatabaseProvider) {
	c := &nodeClient{svc: svc, agentID: agentID, timeout: 2 * time.Minute}
	return (*localContainers)(c), &localDB{c: c, fs: local}
}

type localContainers nodeClient

func (c *localContainers) nc() *nodeClient { return (*nodeClient)(c) }
func (c *localContainers) Create(ctx context.Context, spec providers.ContainerSpec) (providers.ContainerInfo, bool, error) {
	var out struct {
		Container providers.ContainerInfo `json:"container"`
		Created   bool                    `json:"created"`
	}
	err := c.nc().call(ctx, "docker.create", spec, &out)
	return out.Container, out.Created, err
}
func (c *localContainers) simple(ctx context.Context, op, name string) error {
	return c.nc().call(ctx, op, map[string]string{"name": name}, nil)
}
func (c *localContainers) Start(ctx context.Context, n string) error {
	return c.simple(ctx, "docker.start", n)
}
func (c *localContainers) Stop(ctx context.Context, n string) error {
	return c.simple(ctx, "docker.stop", n)
}
func (c *localContainers) Restart(ctx context.Context, n string) error {
	return c.simple(ctx, "docker.restart", n)
}
func (c *localContainers) Remove(ctx context.Context, n string) error {
	return c.simple(ctx, "docker.remove", n)
}
func (c *localContainers) Inspect(ctx context.Context, n string) (providers.ContainerInfo, bool, error) {
	var out struct {
		Container providers.ContainerInfo `json:"container"`
		Found     bool                    `json:"found"`
	}
	err := c.nc().call(ctx, "docker.inspect", map[string]string{"name": n}, &out)
	return out.Container, out.Found, err
}
func (c *localContainers) List(context.Context, map[string]string) ([]providers.ContainerInfo, error) {
	return nil, errors.New("listing containers is not part of the agent allowlist")
}
func (c *localContainers) Logs(ctx context.Context, n string, tail int) ([]string, error) {
	var out struct {
		Lines []string `json:"lines"`
	}
	err := c.nc().call(ctx, "docker.logs", map[string]any{"name": n, "tail": tail}, &out)
	return out.Lines, err
}

type localDB struct {
	c  *nodeClient
	fs providers.FilesystemProvider
}

const tmpDir = ".tmp-db"

func (d *localDB) EnsureDatabase(ctx context.Context, name, user, password string) (bool, error) {
	var out struct {
		Created bool `json:"created"`
	}
	err := d.c.call(ctx, "database.ensure", map[string]string{"name": name, "user": user, "password": password}, &out)
	return out.Created, err
}
func (d *localDB) DropDatabase(ctx context.Context, name, user string) error {
	return d.c.call(ctx, "database.drop", map[string]string{"name": name, "user": user}, nil)
}
func (d *localDB) Exists(ctx context.Context, name string) (bool, error) {
	var out struct {
		Exists bool `json:"exists"`
	}
	err := d.c.call(ctx, "database.exists", map[string]string{"name": name}, &out)
	return out.Exists, err
}

// tempFile reserves a unique relative path in the shared data root.
func (d *localDB) tempFile(ctx context.Context) (rel, abs string, err error) {
	if err := d.fs.MkdirAll(ctx, tmpDir); err != nil {
		return "", "", err
	}
	rel = tmpDir + "/" + uuid.NewString() + ".sql"
	abs, err = d.fs.AbsPath(rel)
	return rel, abs, err
}

func (d *localDB) Dump(ctx context.Context, name string, w io.Writer) error {
	rel, abs, err := d.tempFile(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(abs) }()
	if err := d.c.call(ctx, "database.dump", map[string]string{"name": name, "path": rel}, nil); err != nil {
		return err
	}
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func (d *localDB) Load(ctx context.Context, name string, r io.Reader) error {
	rel, abs, err := d.tempFile(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(abs) }()
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return d.c.call(ctx, "database.load", map[string]string{"name": name, "path": rel}, nil)
}

var _ providers.DatabaseProvider = (*localDB)(nil)
