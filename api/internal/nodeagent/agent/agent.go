// Package agent is the Node Agent runtime: it enrols with the control plane,
// then loops heartbeat + poll, verifying and executing signed commands. It
// only ever makes outbound HTTPS requests (no inbound port is opened on the
// node).
package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nodera/nodera/internal/nodeagent/executor"
	"github.com/nodera/nodera/internal/nodeagent/protocol"
	"github.com/nodera/nodera/internal/providers"
)

const Version = "0.1.0"

// State is persisted (0600) after enrolment.
type State struct {
	AgentID         string `json:"agent_id"`
	PrivateSeedB64  string `json:"private_seed"`
	ServerPublicKey string `json:"server_public_key"`
	APIURL          string `json:"api_url"`
}

func LoadState(path string) (State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var s State
	return s, json.Unmarshal(b, &s)
}

func SaveState(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(path, b, 0o600)
}

type Agent struct {
	state    State
	priv     ed25519.PrivateKey
	serverPK ed25519.PublicKey
	http     *http.Client
	exec     *executor.Executor
	caps     []string
	Now      func() time.Time
	// OnCommand is an optional observer (tests/logging).
	OnCommand func(cmd protocol.Command, err error)
}

func New(state State, set providers.Set, client *http.Client) (*Agent, error) {
	seed, err := base64.StdEncoding.DecodeString(state.PrivateSeedB64)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid private key in state")
	}
	spk, err := base64.StdEncoding.DecodeString(state.ServerPublicKey)
	if err != nil || len(spk) != ed25519.PublicKeySize {
		return nil, errors.New("invalid server public key in state")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	var caps []string
	if set.Containers != nil {
		caps = append(caps, "docker")
	}
	if set.FS != nil {
		caps = append(caps, "filesystem")
	}
	if set.Monitoring != nil {
		caps = append(caps, "metrics")
	}
	return &Agent{state: state, priv: ed25519.NewKeyFromSeed(seed), serverPK: spk, http: client, exec: &executor.Executor{P: set}, caps: caps, Now: time.Now}, nil
}

// Enroll registers a fresh key pair using a one-time token and returns the
// state to persist. The private key is generated here and never transmitted.
func Enroll(ctx context.Context, apiURL, token string, client *http.Client) (State, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return State{}, err
	}
	body, _ := json.Marshal(protocol.EnrollRequest{Token: token, PublicKeyB64: base64.StdEncoding.EncodeToString(pub), Version: Version, Capabilities: []string{}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/agent/v1/enroll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return State{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return State{}, fmt.Errorf("enrolment failed (%d): %s", resp.StatusCode, string(data))
	}
	var er protocol.EnrollResponse
	if err := json.Unmarshal(data, &er); err != nil {
		return State{}, err
	}
	return State{AgentID: er.AgentID, ServerPublicKey: er.ServerPublicKey, APIURL: apiURL, PrivateSeedB64: base64.StdEncoding.EncodeToString(priv.Seed())}, nil
}

func (a *Agent) do(ctx context.Context, method, path string, payload any, out any) error {
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	nonceRaw := make([]byte, 18)
	_, _ = rand.Read(nonceRaw)
	hdrs := protocol.SignRequest(a.priv, a.state.AgentID, method, path, body, a.Now(), base64.RawURLEncoding.EncodeToString(nonceRaw))
	req, _ := http.NewRequestWithContext(ctx, method, a.state.APIURL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, string(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Step performs one heartbeat and one poll/execute cycle (used by Run and tests).
func (a *Agent) Step(ctx context.Context) error {
	hb := protocol.Heartbeat{Version: Version, Capabilities: a.caps, Status: "online"}
	if a.exec.P.Monitoring != nil {
		if m, err := a.exec.P.Monitoring.CollectNodeMetrics(ctx, "self"); err == nil {
			hb.Metrics = map[string]float64{}
			if m.RAMTotalMB > 0 {
				hb.Metrics["ram"] = m.RAMUsedMB * 100 / m.RAMTotalMB
			}
			if m.DiskTotalGB > 0 {
				hb.Metrics["disk"] = m.DiskUsedGB * 100 / m.DiskTotalGB
			}
			hb.Metrics["cpu"] = m.CPUPercent
		}
	}
	if err := a.do(ctx, http.MethodPost, "/agent/v1/heartbeat", hb, nil); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	var cmds []protocol.Command
	if err := a.do(ctx, http.MethodPost, "/agent/v1/poll", struct{}{}, &cmds); err != nil {
		return fmt.Errorf("poll: %w", err)
	}
	for _, c := range cmds {
		res := a.handle(ctx, c)
		if err := a.do(ctx, http.MethodPost, "/agent/v1/commands/"+c.ID+"/result", res, nil); err != nil {
			return fmt.Errorf("report result: %w", err)
		}
	}
	return nil
}

func (a *Agent) handle(ctx context.Context, c protocol.Command) protocol.CommandResult {
	if err := protocol.VerifyCommand(a.serverPK, a.state.AgentID, c, a.Now()); err != nil {
		if a.OnCommand != nil {
			a.OnCommand(c, err)
		}
		return protocol.CommandResult{OK: false, Error: "rejected: " + err.Error()}
	}
	out, err := a.exec.Execute(ctx, c)
	if a.OnCommand != nil {
		a.OnCommand(c, err)
	}
	if err != nil {
		return protocol.CommandResult{OK: false, Error: err.Error()}
	}
	return protocol.CommandResult{OK: true, Result: out}
}

// Run loops until ctx is cancelled.
func (a *Agent) Run(ctx context.Context, interval time.Duration, logf func(format string, args ...any)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := a.Step(ctx); err != nil && logf != nil {
			logf("agent step failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
