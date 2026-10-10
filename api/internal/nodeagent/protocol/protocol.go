// Package protocol defines the Node Agent wire protocol shared by the Nodera
// API and the agent binary: request signing, command signing, and the
// command allowlist with per-operation parameter validation.
//
// Security model (see docs/NODE-AGENT.md):
//
//   - The agent holds an Ed25519 private key that never leaves the node. The
//     API stores only the public key (registered during one-time enrolment).
//   - Every agent->API request is signed over method, path, a timestamp, a
//     random nonce and the SHA-256 of the body. The API rejects stale
//     timestamps and any reused nonce (replay protection).
//   - The API signs every command with its own Ed25519 key; the agent
//     verifies it, checks expiry, and checks the operation against the
//     allowlist. There is no "run shell command" operation and none can be
//     added without extending the allowlist in this package.
package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	HeaderAgentID   = "X-Nodera-Agent"
	HeaderTimestamp = "X-Nodera-Timestamp"
	HeaderNonce     = "X-Nodera-Nonce"
	HeaderSignature = "X-Nodera-Signature"

	// MaxClockSkew is how far a request timestamp may deviate from server time.
	MaxClockSkew = 60 * time.Second
)

// ---- Request signing (agent -> API) ----

func BodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func requestMessage(agentID, ts, nonce, method, p, bodyHash string) []byte {
	return []byte(strings.Join([]string{"NODERA-AGENT-V1", agentID, ts, nonce, strings.ToUpper(method), p, bodyHash}, "\n"))
}

// SignRequest returns the headers an agent must attach.
func SignRequest(priv ed25519.PrivateKey, agentID, method, p string, body []byte, now time.Time, nonce string) map[string]string {
	ts := fmt.Sprintf("%d", now.Unix())
	sig := ed25519.Sign(priv, requestMessage(agentID, ts, nonce, method, p, BodyHash(body)))
	return map[string]string{
		HeaderAgentID: agentID, HeaderTimestamp: ts, HeaderNonce: nonce,
		HeaderSignature: base64.StdEncoding.EncodeToString(sig),
	}
}

var nonceRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// VerifyRequest validates timestamp freshness, nonce shape and the signature.
// Nonce uniqueness (replay) is enforced by the caller against storage.
func VerifyRequest(pub ed25519.PublicKey, hdr func(string) string, method, p string, body []byte, now time.Time) error {
	agentID, ts, nonce, sigB64 := hdr(HeaderAgentID), hdr(HeaderTimestamp), hdr(HeaderNonce), hdr(HeaderSignature)
	if agentID == "" || ts == "" || nonce == "" || sigB64 == "" {
		return errors.New("missing signature headers")
	}
	if !nonceRe.MatchString(nonce) {
		return errors.New("malformed nonce")
	}
	var sec int64
	if _, err := fmt.Sscanf(ts, "%d", &sec); err != nil {
		return errors.New("malformed timestamp")
	}
	d := now.Sub(time.Unix(sec, 0))
	if d < -MaxClockSkew || d > MaxClockSkew {
		return errors.New("timestamp outside the allowed window")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("malformed signature")
	}
	if !ed25519.Verify(pub, requestMessage(agentID, ts, nonce, method, p, BodyHash(body)), sig) {
		return errors.New("invalid signature")
	}
	return nil
}

// ---- Command signing (API -> agent) ----

// Command is delivered to an agent in the poll response.
type Command struct {
	ID        string          `json:"id"`
	AgentID   string          `json:"agent_id"`
	RequestID string          `json:"request_id"`
	Op        string          `json:"op"`
	Params    json.RawMessage `json:"params"`
	ExpiresAt int64           `json:"expires_at"` // unix seconds
	Signature string          `json:"signature"`  // base64 Ed25519
}

// Canonical re-encodes JSON with sorted keys and preserved number text so the
// signer and verifier hash identical bytes regardless of storage round-trips.
func Canonical(raw json.RawMessage) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{}"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v) // encoding/json sorts map keys
}

func commandMessage(c Command, canonParams []byte) []byte {
	return []byte(strings.Join([]string{"NODERA-CMD-V1", c.ID, c.AgentID, c.RequestID, c.Op, fmt.Sprintf("%d", c.ExpiresAt), BodyHash(canonParams)}, "\n"))
}

func SignCommand(priv ed25519.PrivateKey, c Command) (Command, error) {
	canon, err := Canonical(c.Params)
	if err != nil {
		return c, err
	}
	c.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, commandMessage(c, canon)))
	return c, nil
}

// VerifyCommand is what the agent runs on every received command: signature,
// addressing (the command must be for this agent), expiry, then allowlist.
func VerifyCommand(pub ed25519.PublicKey, expectAgentID string, c Command, now time.Time) error {
	if c.AgentID != expectAgentID {
		return errors.New("command addressed to a different agent")
	}
	if now.Unix() > c.ExpiresAt {
		return errors.New("command expired")
	}
	canon, err := Canonical(c.Params)
	if err != nil {
		return errors.New("malformed params")
	}
	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	if err != nil || !ed25519.Verify(pub, commandMessage(c, canon), sig) {
		return errors.New("invalid command signature")
	}
	return ValidateParams(c.Op, c.Params)
}

// ---- Allowlist ----

// Allowed lists every operation an agent will ever execute. Adding an
// operation requires adding a validator here — there is no generic exec.
var Allowed = map[string]func(p map[string]any) error{
	"docker.create":     validDockerCreate,
	"docker.start":      needName,
	"docker.stop":       needName,
	"docker.restart":    needName,
	"docker.remove":     needName,
	"docker.inspect":    needName,
	"docker.logs":       needName,
	"filesystem.read":   needPath,
	"filesystem.write":  validFSWrite,
	"filesystem.mkdir":  needPath,
	"filesystem.remove": needPath,
	"service.health":    validHealth,
	"metrics.collect":   func(map[string]any) error { return nil },
}

// ValidateParams rejects unknown operations and malformed/unsafe parameters.
func ValidateParams(op string, raw json.RawMessage) error {
	v, ok := Allowed[op]
	if !ok {
		return fmt.Errorf("operation %q is not in the allowlist", op)
	}
	var p map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&p); err != nil {
			return errors.New("params must be a JSON object")
		}
	}
	if p == nil {
		p = map[string]any{}
	}
	return v(p)
}

var (
	nameRe  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	imageRe = regexp.MustCompile(`^[a-z0-9]+([._/:@-][a-zA-Z0-9]+)*$`)
)

func str(p map[string]any, k string) string {
	s, _ := p[k].(string)
	return s
}
func needName(p map[string]any) error {
	if !nameRe.MatchString(str(p, "name")) {
		return errors.New("invalid or missing name")
	}
	return nil
}
func SafeRelPath(s string) error {
	if s == "" || path.IsAbs(s) || strings.ContainsRune(s, 0) {
		return errors.New("path must be a non-empty relative path")
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return errors.New("path must not contain ..")
		}
	}
	return nil
}
func needPath(p map[string]any) error { return SafeRelPath(str(p, "path")) }
func validFSWrite(p map[string]any) error {
	if err := SafeRelPath(str(p, "path")); err != nil {
		return err
	}
	if len(str(p, "content_b64")) > 8<<20 {
		return errors.New("content too large (max 8 MiB)")
	}
	return nil
}
func validDockerCreate(p map[string]any) error {
	if err := needName(p); err != nil {
		return err
	}
	img := str(p, "image")
	if len(img) > 255 || !imageRe.MatchString(img) || strings.HasPrefix(img, "-") {
		return errors.New("invalid image")
	}
	return nil
}
func validHealth(p map[string]any) error {
	switch str(p, "kind") {
	case "http":
		u, err := url.Parse(str(p, "target"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("invalid http target")
		}
	case "tcp":
		if !strings.Contains(str(p, "target"), ":") {
			return errors.New("tcp target must be host:port")
		}
	default:
		return errors.New("kind must be http or tcp")
	}
	return nil
}

// ---- Wire types ----

type EnrollRequest struct {
	Token        string   `json:"token"`
	PublicKeyB64 string   `json:"public_key"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}
type EnrollResponse struct {
	AgentID         string `json:"agent_id"`
	ServerPublicKey string `json:"server_public_key"` // base64 Ed25519 (command signing key)
}
type Heartbeat struct {
	Version      string             `json:"version"`
	Capabilities []string           `json:"capabilities"`
	Status       string             `json:"status"` // online | degraded
	Metrics      map[string]float64 `json:"metrics,omitempty"`
	Detail       map[string]any     `json:"detail,omitempty"`
}
type CommandResult struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}
