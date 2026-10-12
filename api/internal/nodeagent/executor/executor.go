// Package executor maps allowlisted agent commands onto provider calls. It is
// the only place agent commands turn into side effects, and it re-validates
// every command (defence in depth: the control plane validates too).
package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nodera/nodera/internal/nodeagent/guard"
	"github.com/nodera/nodera/internal/nodeagent/protocol"
	"github.com/nodera/nodera/internal/providers"
)

type Executor struct {
	P providers.Set
	// Guard decides which containers/mounts are off-limits (control plane).
	Guard guard.Policy
}

const maxRead = 4 << 20

// Execution time limits. A signed command has a delivery expiry, but once it is
// running nothing else would stop a hung `docker`/`mariadb-dump` process, so each
// class of operation gets a hard ceiling.
const (
	defaultTimeout = 5 * time.Minute
	dataTimeout    = 2 * time.Hour // database dump/load of a large site
)

func timeoutFor(op string) time.Duration {
	if op == "database.dump" || op == "database.load" {
		return dataTimeout
	}
	return defaultTimeout
}

func (e *Executor) Execute(ctx context.Context, cmd protocol.Command) (json.RawMessage, error) {
	if err := protocol.ValidateParams(cmd.Op, cmd.Params); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeoutFor(cmd.Op))
	defer cancel()
	var p struct {
		Name       string                  `json:"name"`
		Image      string                  `json:"image"`
		Env        map[string]string       `json:"env"`
		Labels     map[string]string       `json:"labels"`
		Ports      []providers.PortMapping `json:"ports"`
		Networks   []string                `json:"networks"`
		Volumes    []providers.VolumeMount `json:"volumes"`
		Cmd        []string                `json:"cmd"`
		Tail       int                     `json:"tail"`
		Path       string                  `json:"path"`
		ContentB64 string                  `json:"content_b64"`
		Kind       string                  `json:"kind"`
		Target     string                  `json:"target"`
		Expect     int                     `json:"expect_status"`
		User       string                  `json:"user"`
		Password   string                  `json:"password"`
	}
	if err := json.Unmarshal(cmd.Params, &p); err != nil {
		return nil, errors.New("malformed params")
	}
	need := func(ok bool, what string) error {
		if !ok {
			return fmt.Errorf("%s provider is not available on this node", what)
		}
		return nil
	}
	switch cmd.Op {
	case "docker.create":
		if err := need(e.P.Containers != nil, "container"); err != nil {
			return nil, err
		}
		var vols [][2]string
		for _, v := range p.Volumes {
			vols = append(vols, [2]string{v.Source, v.Target})
		}
		if err := e.Guard.CheckCreate(p.Name, p.Labels, p.Networks, vols); err != nil {
			return nil, err
		}
		info, created, err := e.P.Containers.Create(ctx, providers.ContainerSpec{Name: p.Name, Image: p.Image, Env: p.Env, Labels: p.Labels, Ports: p.Ports, Networks: p.Networks, Volumes: p.Volumes, Cmd: p.Cmd, User: p.User})
		return marshal(map[string]any{"container": info, "created": created}, err)
	case "docker.start", "docker.stop", "docker.restart", "docker.remove":
		if err := need(e.P.Containers != nil, "container"); err != nil {
			return nil, err
		}
		if err := e.checkMutable(ctx, p.Name); err != nil {
			return nil, err
		}
		var err error
		switch cmd.Op {
		case "docker.start":
			err = e.P.Containers.Start(ctx, p.Name)
		case "docker.stop":
			err = e.P.Containers.Stop(ctx, p.Name)
		case "docker.restart":
			err = e.P.Containers.Restart(ctx, p.Name)
		default:
			err = e.P.Containers.Remove(ctx, p.Name)
		}
		return marshal(map[string]any{"ok": true}, err)
	case "docker.inspect":
		if err := need(e.P.Containers != nil, "container"); err != nil {
			return nil, err
		}
		info, found, err := e.P.Containers.Inspect(ctx, p.Name)
		return marshal(map[string]any{"container": info, "found": found}, err)
	case "docker.logs":
		if err := need(e.P.Containers != nil, "container"); err != nil {
			return nil, err
		}
		lines, err := e.P.Containers.Logs(ctx, p.Name, p.Tail)
		return marshal(map[string]any{"lines": lines}, err)
	case "database.ensure", "database.drop", "database.exists", "database.dump", "database.load":
		if err := need(e.P.DB != nil, "database"); err != nil {
			return nil, err
		}
		return e.database(ctx, cmd.Op, p.Name, p.User, p.Password, p.Path)
	case "filesystem.read":
		if err := need(e.P.FS != nil, "filesystem"); err != nil {
			return nil, err
		}
		b, err := e.P.FS.ReadFile(ctx, p.Path)
		if err == nil && len(b) > maxRead {
			return nil, errors.New("file too large to read through the agent")
		}
		return marshal(map[string]any{"content_b64": base64.StdEncoding.EncodeToString(b)}, err)
	case "filesystem.write":
		if err := need(e.P.FS != nil, "filesystem"); err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(p.ContentB64)
		if err != nil {
			return nil, errors.New("content_b64 is not valid base64")
		}
		return marshal(map[string]any{"ok": true}, e.P.FS.WriteFile(ctx, p.Path, data))
	case "filesystem.mkdir":
		if err := need(e.P.FS != nil, "filesystem"); err != nil {
			return nil, err
		}
		return marshal(map[string]any{"ok": true}, e.P.FS.MkdirAll(ctx, p.Path))
	case "filesystem.remove":
		if err := need(e.P.FS != nil, "filesystem"); err != nil {
			return nil, err
		}
		return marshal(map[string]any{"ok": true}, e.P.FS.Remove(ctx, p.Path))
	case "service.health":
		if err := need(e.P.Monitoring != nil, "monitoring"); err != nil {
			return nil, err
		}
		var res providers.CheckResult
		var err error
		if p.Kind == "http" {
			res, err = e.P.Monitoring.CheckHTTP(ctx, p.Target, p.Expect)
		} else {
			res, err = e.P.Monitoring.CheckTCP(ctx, p.Target)
		}
		return marshal(res, err)
	case "metrics.collect":
		if err := need(e.P.Monitoring != nil, "monitoring"); err != nil {
			return nil, err
		}
		m, err := e.P.Monitoring.CollectNodeMetrics(ctx, "self")
		return marshal(m, err)
	}
	return nil, fmt.Errorf("operation %q is not implemented by this agent", cmd.Op)
}

// checkMutable refuses lifecycle changes to protected containers, consulting the
// live container's labels as well as the configured names.
func (e *Executor) checkMutable(ctx context.Context, name string) error {
	var labels map[string]string
	if info, found, err := e.P.Containers.Inspect(ctx, name); err == nil && found {
		labels = info.Labels
	}
	return e.Guard.CheckMutation(name, labels)
}

// database runs allowlisted database operations. Dumps and loads move data
// through a file under the shared data root rather than the command channel,
// so memory stays bounded however large the database is.
func (e *Executor) database(ctx context.Context, op, name, user, password, rel string) (json.RawMessage, error) {
	db := e.P.DB
	switch op {
	case "database.ensure":
		created, err := db.EnsureDatabase(ctx, name, user, password)
		return marshal(map[string]any{"created": created}, err)
	case "database.drop":
		return marshal(map[string]any{"ok": true}, db.DropDatabase(ctx, name, user))
	case "database.exists":
		ok, err := db.Exists(ctx, name)
		return marshal(map[string]any{"exists": ok}, err)
	}
	if e.P.FS == nil {
		return nil, errors.New("filesystem provider is not available on this node")
	}
	abs, err := e.P.FS.AbsPath(rel) // enforces containment in the data root
	if err != nil {
		return nil, err
	}
	if op == "database.dump" {
		f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		derr := db.Dump(ctx, name, f)
		if cerr := f.Close(); derr == nil {
			derr = cerr
		}
		if derr != nil {
			_ = os.Remove(abs)
			return nil, derr
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{"bytes": st.Size()}, nil)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return marshal(map[string]any{"ok": true}, db.Load(ctx, name, f))
}

func marshal(v any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	return b, err
}
