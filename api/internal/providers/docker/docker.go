// Package docker is the Docker CLI adapter for providers.ContainerProvider.
// It never uses a shell: every command is an argv slice, and every
// caller-supplied name/image/label/env key is validated against a strict
// pattern first, so command and argument injection are impossible by
// construction. It is exercised in tests through the Runner seam; running it
// against a real Docker daemon is part of the Hetzner/node integration step.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nodera/nodera/internal/providers"
)

// Runner executes a docker command (argv, no shell).
type Runner interface {
	Run(ctx context.Context, args ...string) (stdout string, err error)
}

type execRunner struct{ bin string }

func (r execRunner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, r.bin, args...) // #nosec G204 -- argv only, args validated by callers
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

type Provider struct{ r Runner }

// New returns a Provider that shells out to the docker binary at bin ("docker" if empty).
func New(bin string) *Provider {
	if bin == "" {
		bin = "docker"
	}
	return &Provider{r: execRunner{bin: bin}}
}

// NewWithRunner is used by tests.
func NewWithRunner(r Runner) *Provider { return &Provider{r: r} }

var (
	nameRe  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	imageRe = regexp.MustCompile(`^[a-z0-9]+([._/:@-][a-zA-Z0-9]+)*$`)
	keyRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)
)

func validate(spec providers.ContainerSpec) error {
	if !nameRe.MatchString(spec.Name) {
		return fmt.Errorf("invalid container name %q", spec.Name)
	}
	if len(spec.Image) > 255 || !imageRe.MatchString(spec.Image) || strings.HasPrefix(spec.Image, "-") {
		return fmt.Errorf("invalid image %q", spec.Image)
	}
	for k := range spec.Env {
		if !keyRe.MatchString(k) {
			return fmt.Errorf("invalid env key %q", k)
		}
	}
	for k := range spec.Labels {
		if !keyRe.MatchString(k) {
			return fmt.Errorf("invalid label key %q", k)
		}
	}
	for _, n := range spec.Networks {
		if !nameRe.MatchString(n) {
			return fmt.Errorf("invalid network %q", n)
		}
	}
	for _, v := range spec.Volumes {
		if strings.ContainsAny(v.Source+v.Target, ":\n\x00") || !strings.HasPrefix(v.Target, "/") {
			return fmt.Errorf("invalid volume mount %q -> %q", v.Source, v.Target)
		}
	}
	return nil
}

type inspectDoc struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status    string `json:"Status"`
		StartedAt string `json:"StartedAt"`
		Health    *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

func toInfo(d inspectDoc) providers.ContainerInfo {
	state := d.State.Status
	switch {
	case d.State.Health != nil && d.State.Health.Status == "unhealthy":
		state = "unhealthy"
	case state == "exited":
		state = "stopped"
	}
	info := providers.ContainerInfo{ID: d.ID, Name: strings.TrimPrefix(d.Name, "/"), Image: d.Config.Image, State: state, Labels: d.Config.Labels}
	if t, err := time.Parse(time.RFC3339Nano, d.State.StartedAt); err == nil && !t.IsZero() && t.Year() > 1 {
		info.StartedAt = &t
	}
	return info
}

func (p *Provider) Inspect(ctx context.Context, name string) (providers.ContainerInfo, bool, error) {
	if !nameRe.MatchString(name) {
		return providers.ContainerInfo{}, false, fmt.Errorf("invalid container name %q", name)
	}
	out, err := p.r.Run(ctx, "inspect", "--type", "container", name)
	if err != nil {
		if strings.Contains(err.Error(), "No such") {
			return providers.ContainerInfo{}, false, nil
		}
		return providers.ContainerInfo{}, false, err
	}
	var docs []inspectDoc
	if err := json.Unmarshal([]byte(out), &docs); err != nil || len(docs) == 0 {
		return providers.ContainerInfo{}, false, fmt.Errorf("unexpected docker inspect output")
	}
	return toInfo(docs[0]), true, nil
}

func (p *Provider) Create(ctx context.Context, spec providers.ContainerSpec) (providers.ContainerInfo, bool, error) {
	if err := validate(spec); err != nil {
		return providers.ContainerInfo{}, false, err
	}
	if existing, ok, err := p.Inspect(ctx, spec.Name); err != nil {
		return providers.ContainerInfo{}, false, err
	} else if ok {
		if existing.Image != spec.Image {
			return providers.ContainerInfo{}, false, fmt.Errorf("container %q exists with a different image (%s)", spec.Name, existing.Image)
		}
		return existing, false, nil
	}
	args := []string{"create", "--name", spec.Name}
	for k, v := range spec.Env {
		args = append(args, "--env", k+"="+v)
	}
	for k, v := range spec.Labels {
		args = append(args, "--label", k+"="+v)
	}
	for _, pm := range spec.Ports {
		args = append(args, "--publish", strconv.Itoa(pm.Host)+":"+strconv.Itoa(pm.Container))
	}
	if len(spec.Networks) > 0 {
		args = append(args, "--network", spec.Networks[0])
	}
	for _, v := range spec.Volumes {
		m := v.Source + ":" + v.Target
		if v.ReadOnly {
			m += ":ro"
		}
		args = append(args, "--volume", m)
	}
	args = append(args, "--", spec.Image)
	args = append(args, spec.Cmd...)
	if _, err := p.r.Run(ctx, args...); err != nil {
		return providers.ContainerInfo{}, false, err
	}
	info, _, err := p.Inspect(ctx, spec.Name)
	return info, true, err
}

func (p *Provider) simple(ctx context.Context, verb, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid container name %q", name)
	}
	_, err := p.r.Run(ctx, verb, name)
	return err
}
func (p *Provider) Start(ctx context.Context, n string) error   { return p.simple(ctx, "start", n) }
func (p *Provider) Stop(ctx context.Context, n string) error    { return p.simple(ctx, "stop", n) }
func (p *Provider) Restart(ctx context.Context, n string) error { return p.simple(ctx, "restart", n) }
func (p *Provider) Remove(ctx context.Context, n string) error {
	if !nameRe.MatchString(n) {
		return fmt.Errorf("invalid container name %q", n)
	}
	if _, err := p.r.Run(ctx, "rm", "--force", n); err != nil && !strings.Contains(err.Error(), "No such") {
		return err
	}
	return nil
}

func (p *Provider) List(ctx context.Context, labels map[string]string) ([]providers.ContainerInfo, error) {
	args := []string{"ps", "--all", "--quiet", "--no-trunc"}
	for k, v := range labels {
		if !keyRe.MatchString(k) || strings.ContainsAny(v, "\n\x00") {
			return nil, fmt.Errorf("invalid label filter")
		}
		args = append(args, "--filter", "label="+k+"="+v)
	}
	out, err := p.r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var res []providers.ContainerInfo
	for _, id := range strings.Fields(out) {
		raw, err := p.r.Run(ctx, "inspect", "--type", "container", id)
		if err != nil {
			continue
		}
		var docs []inspectDoc
		if json.Unmarshal([]byte(raw), &docs) == nil && len(docs) > 0 {
			res = append(res, toInfo(docs[0]))
		}
	}
	return res, nil
}

func (p *Provider) Logs(ctx context.Context, name string, tail int) ([]string, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid container name %q", name)
	}
	if tail <= 0 || tail > 5000 {
		tail = 200
	}
	out, err := p.r.Run(ctx, "logs", "--tail", strconv.Itoa(tail), name)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(out, "\n"), "\n"), nil
}
