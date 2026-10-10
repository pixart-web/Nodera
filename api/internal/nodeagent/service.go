// Package nodeagent is the control-plane side of the Node Agent: enrolment,
// signed-request authentication with replay protection, heartbeats, and the
// signed command queue. The agent itself lives in cmd/agent.
package nodeagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/nodeagent/protocol"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
)

const permManage = "infrastructure.manage"
const permRead = "infrastructure.read"

// OfflineAfter is how long without a heartbeat before an agent is OFFLINE.
const OfflineAfter = 90 * time.Second

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool    *pgxpool.Pool
	audit   AuditRecorder
	signKey ed25519.PrivateKey
	now     func() time.Time
}

// New builds the service. signKey signs every command; nil generates an
// ephemeral key (development only — pending commands do not survive restarts).
func New(pool *pgxpool.Pool, auditRecorder AuditRecorder, signKey ed25519.PrivateKey) *Service {
	if signKey == nil {
		_, signKey, _ = ed25519.GenerateKey(rand.Reader)
	}
	return &Service{pool: pool, audit: auditRecorder, signKey: signKey, now: time.Now}
}

// KeyFromSeed derives the command-signing key from a base64 32-byte seed.
func KeyFromSeed(seedB64 string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(seedB64)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("agent signing key must be a base64-encoded 32-byte seed")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func (s *Service) ServerPublicKey() string {
	return base64.StdEncoding.EncodeToString(s.signKey.Public().(ed25519.PublicKey))
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

type Agent struct {
	ID             uuid.UUID       `json:"id"`
	OrganizationID uuid.UUID       `json:"organization_id"`
	NodeID         uuid.UUID       `json:"node_id"`
	Hostname       string          `json:"hostname,omitempty"`
	Status         string          `json:"status"`
	Version        string          `json:"version"`
	Capabilities   []string        `json:"capabilities"`
	LastSeenAt     *time.Time      `json:"last_seen_at,omitempty"`
	LastHeartbeat  json.RawMessage `json:"last_heartbeat,omitempty"`
	RegisteredAt   time.Time       `json:"registered_at"`
	RevokedAt      *time.Time      `json:"revoked_at,omitempty"`
	publicKey      ed25519.PublicKey
}

func (s *Service) sysCtx(orgID uuid.UUID, label string) authctx.AuthContext {
	ac := authctx.System(orgID)
	ac.ActorLabel = label
	return ac
}

func (s *Service) rec(ctx context.Context, ac authctx.AuthContext, action, resType, resID string, ok bool, meta map[string]any) {
	if err := s.audit.Record(ctx, ac, audit.Entry{Action: action, ResourceType: resType, ResourceID: resID, Success: ok, Metadata: meta}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
}

// ---- Enrolment ----

type Registration struct {
	Token     string    `json:"token"` // shown exactly once
	ExpiresAt time.Time `json:"expires_at"`
	NodeID    uuid.UUID `json:"node_id"`
}

// CreateRegistration mints a one-time enrolment token for a node.
func (s *Service) CreateRegistration(ctx context.Context, ac authctx.AuthContext, nodeID uuid.UUID) (Registration, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Registration{}, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM nodes WHERE id = $1 AND organization_id = $2 AND status <> 'decommissioned')`,
		nodeID, ac.OrganizationID).Scan(&exists); err != nil {
		return Registration{}, apierr.Wrap(apierr.CodeInternal, "failed to look up node", err)
	}
	if !exists {
		return Registration{}, apierr.NotFound("node")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Registration{}, apierr.Wrap(apierr.CodeInternal, "failed to generate token", err)
	}
	token := "ndr_enr_" + base64.RawURLEncoding.EncodeToString(raw)
	exp := s.now().Add(30 * time.Minute)
	var uid *uuid.UUID
	if ac.ActorType == authctx.ActorUser {
		id := ac.ActorID
		uid = &id
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO node_agent_registrations (organization_id, node_id, token_hash, expires_at, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5)`, ac.OrganizationID, nodeID, hashToken(token), exp, uid); err != nil {
		return Registration{}, apierr.Wrap(apierr.CodeInternal, "failed to store registration", err)
	}
	s.rec(ctx, ac, "nodeagent.registration.created", "node", nodeID.String(), true, nil) // never the token itself
	return Registration{Token: token, ExpiresAt: exp, NodeID: nodeID}, nil
}

// Enroll exchanges a one-time token for an agent identity. The token is
// consumed atomically, so a replayed or raced enrolment fails.
func (s *Service) Enroll(ctx context.Context, req protocol.EnrollRequest) (protocol.EnrollResponse, error) {
	pub, err := base64.StdEncoding.DecodeString(req.PublicKeyB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return protocol.EnrollResponse{}, apierr.Validation("public_key must be a base64 Ed25519 public key")
	}
	if len(req.Version) > 64 {
		return protocol.EnrollResponse{}, apierr.Validation("version too long")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return protocol.EnrollResponse{}, apierr.Wrap(apierr.CodeInternal, "failed to begin", err)
	}
	defer tx.Rollback(ctx)

	var orgID, nodeID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE node_agent_registrations SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING organization_id, node_id`, hashToken(req.Token)).Scan(&orgID, &nodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return protocol.EnrollResponse{}, apierr.Unauthenticated("invalid, expired or already used enrolment token")
	}
	if err != nil {
		return protocol.EnrollResponse{}, apierr.Wrap(apierr.CodeInternal, "failed to consume token", err)
	}
	var existingStatus string
	switch err := tx.QueryRow(ctx, `SELECT status FROM node_agents WHERE node_id = $1 FOR UPDATE`, nodeID).Scan(&existingStatus); {
	case err == nil && existingStatus != "revoked":
		return protocol.EnrollResponse{}, apierr.Conflict("an agent is already enrolled for this node; revoke it first")
	case err == nil:
		if _, err := tx.Exec(ctx, `DELETE FROM node_agents WHERE node_id = $1`, nodeID); err != nil {
			return protocol.EnrollResponse{}, apierr.Wrap(apierr.CodeInternal, "failed to replace revoked agent", err)
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return protocol.EnrollResponse{}, apierr.Wrap(apierr.CodeInternal, "failed to check existing agent", err)
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO node_agents (organization_id, node_id, public_key, version, capabilities)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, orgID, nodeID, pub, req.Version, nonNil(req.Capabilities)).Scan(&id); err != nil {
		return protocol.EnrollResponse{}, apierr.Wrap(apierr.CodeInternal, "failed to create agent", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return protocol.EnrollResponse{}, apierr.Wrap(apierr.CodeInternal, "failed to commit", err)
	}
	s.rec(ctx, s.sysCtx(orgID, "node-agent"), "nodeagent.agent.enrolled", "node_agent", id.String(), true, map[string]any{"node_id": nodeID.String(), "version": req.Version})
	return protocol.EnrollResponse{AgentID: id.String(), ServerPublicKey: s.ServerPublicKey()}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- Authentication ----

var errBadCreds = apierr.Unauthenticated("invalid agent credentials")

// Authenticate verifies a signed agent request and consumes its nonce.
func (s *Service) Authenticate(ctx context.Context, hdr func(string) string, method, path string, body []byte) (Agent, error) {
	id, err := uuid.Parse(hdr(protocol.HeaderAgentID))
	if err != nil {
		return Agent{}, errBadCreds
	}
	a, err := s.load(ctx, uuid.Nil, id)
	if err != nil || a.Status == "revoked" {
		return Agent{}, errBadCreds
	}
	if err := protocol.VerifyRequest(a.publicKey, hdr, method, path, body, s.now()); err != nil {
		logger.FromContext(ctx).Warn("agent request rejected", "agent_id", id, "reason", err.Error())
		return Agent{}, errBadCreds
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO node_agent_nonces (agent_id, nonce) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, hdr(protocol.HeaderNonce))
	if err != nil {
		return Agent{}, apierr.Wrap(apierr.CodeInternal, "failed to record nonce", err)
	}
	if tag.RowsAffected() == 0 {
		logger.FromContext(ctx).Warn("agent replay rejected", "agent_id", id)
		return Agent{}, errBadCreds
	}
	return a, nil
}

const agentCols = `a.id, a.organization_id, a.node_id, n.hostname, a.status, a.version, a.capabilities, a.last_seen_at,
	a.last_heartbeat, a.registered_at, a.revoked_at, a.public_key`

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.ID, &a.OrganizationID, &a.NodeID, &a.Hostname, &a.Status, &a.Version, &a.Capabilities, &a.LastSeenAt,
		&a.LastHeartbeat, &a.RegisteredAt, &a.RevokedAt, &a.publicKey)
	return a, err
}

func (s *Service) load(ctx context.Context, orgID, id uuid.UUID) (Agent, error) {
	q := `SELECT ` + agentCols + ` FROM node_agents a JOIN nodes n ON n.id = a.node_id WHERE a.id = $1`
	args := []any{id}
	if orgID != uuid.Nil {
		q += ` AND a.organization_id = $2`
		args = append(args, orgID)
	}
	a, err := scanAgent(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, apierr.NotFound("agent")
	}
	if err != nil {
		return Agent{}, apierr.Wrap(apierr.CodeInternal, "failed to load agent", err)
	}
	return a, nil
}

// ---- Heartbeat ----

func (s *Service) Heartbeat(ctx context.Context, a Agent, hb protocol.Heartbeat) error {
	status := "online"
	if hb.Status == "degraded" {
		status = "degraded"
	}
	hbJSON, _ := json.Marshal(hb)
	if len(hbJSON) > 16<<10 {
		return apierr.Validation("heartbeat too large")
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE node_agents SET status = $2, version = $3, capabilities = $4, last_seen_at = now(), last_heartbeat = $5 WHERE id = $1
	`, a.ID, status, truncate(hb.Version, 64), nonNil(hb.Capabilities), hbJSON); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to record heartbeat", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE nodes SET status = $2, last_seen_at = now(), updated_at = now() WHERE id = $1 AND status <> 'decommissioned'`, a.NodeID, status); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to update node status", err)
	}
	metricMap := map[string]string{"cpu": "cpu", "ram": "ram", "disk": "disk", "network_up": "network_up", "network_down": "network_down"}
	for k, metric := range metricMap {
		if v, ok := hb.Metrics[k]; ok && v >= 0 {
			if _, err := s.pool.Exec(ctx, `INSERT INTO metric_samples (organization_id, node_id, metric, value) VALUES ($1, $2, $3, $4)`, a.OrganizationID, a.NodeID, metric, v); err != nil {
				logger.FromContext(ctx).Error("failed to store metric", "error", err)
			}
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---- Commands ----

const commandTTL = 5 * time.Minute

type QueuedCommand struct {
	ID        uuid.UUID `json:"id"`
	RequestID string    `json:"request_id"`
	Op        string    `json:"op"`
	Status    string    `json:"status"`
}

// Enqueue validates against the allowlist, signs and queues a command for an
// agent. Callers must already be authorised (API handler or internal engine).
func (s *Service) enqueue(ctx context.Context, orgID, agentID uuid.UUID, op string, params any, jobID *uuid.UUID, requestID string) (QueuedCommand, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return QueuedCommand{}, apierr.Validation("params are not valid JSON")
	}
	if string(raw) == "null" {
		raw = []byte("{}")
	}
	if err := protocol.ValidateParams(op, raw); err != nil {
		return QueuedCommand{}, apierr.Validation(err.Error())
	}
	a, err := s.load(ctx, orgID, agentID)
	if err != nil {
		return QueuedCommand{}, err
	}
	if a.Status == "revoked" {
		return QueuedCommand{}, apierr.Conflict("agent is revoked")
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	id := uuid.New()
	exp := s.now().Add(commandTTL)
	cmd, err := protocol.SignCommand(s.signKey, protocol.Command{ID: id.String(), AgentID: agentID.String(), RequestID: requestID, Op: op, Params: raw, ExpiresAt: exp.Unix()})
	if err != nil {
		return QueuedCommand{}, apierr.Wrap(apierr.CodeInternal, "failed to sign command", err)
	}
	sig, _ := base64.StdEncoding.DecodeString(cmd.Signature)
	canon, _ := protocol.Canonical(raw)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO node_agent_commands (id, organization_id, agent_id, request_id, job_id, op, params, signature, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, orgID, agentID, requestID, jobID, op, canon, sig, exp); err != nil {
		return QueuedCommand{}, apierr.Wrap(apierr.CodeInternal, "failed to queue command", err)
	}
	return QueuedCommand{ID: id, RequestID: requestID, Op: op, Status: "pending"}, nil
}

// EnqueueAs is the authorised entry point for operators (infrastructure.manage).
func (s *Service) EnqueueAs(ctx context.Context, ac authctx.AuthContext, agentID uuid.UUID, op string, params any) (QueuedCommand, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return QueuedCommand{}, err
	}
	q, err := s.enqueue(ctx, ac.OrganizationID, agentID, op, params, nil, ac.CorrelationID)
	if err != nil {
		return QueuedCommand{}, err
	}
	s.rec(ctx, ac, "nodeagent.command.queued", "node_agent", agentID.String(), true, map[string]any{"op": op, "command_id": q.ID.String(), "request_id": q.RequestID})
	return q, nil
}

// Dispatch queues a command on behalf of an operation (engine -> agent) and
// waits for its result. orgID scoping is enforced by the agent lookup.
func (s *Service) Dispatch(ctx context.Context, orgID, agentID uuid.UUID, jobID *uuid.UUID, requestID, op string, params any, timeout time.Duration) (protocol.CommandResult, error) {
	q, err := s.enqueue(ctx, orgID, agentID, op, params, jobID, requestID)
	if err != nil {
		return protocol.CommandResult{}, err
	}
	s.rec(ctx, s.sysCtx(orgID, "operation"), "nodeagent.command.queued", "node_agent", agentID.String(), true, map[string]any{"op": op, "command_id": q.ID.String(), "request_id": q.RequestID})
	return s.WaitResult(ctx, orgID, q.ID, timeout)
}

// WaitResult blocks until a command completes, expires, or ctx/timeout ends.
func (s *Service) WaitResult(ctx context.Context, orgID, cmdID uuid.UUID, timeout time.Duration) (protocol.CommandResult, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		var status string
		var result json.RawMessage
		var errMsg *string
		err := s.pool.QueryRow(ctx, `SELECT status, COALESCE(result, 'null'), error FROM node_agent_commands WHERE id = $1 AND organization_id = $2`, cmdID, orgID).Scan(&status, &result, &errMsg)
		if err != nil {
			return protocol.CommandResult{}, apierr.Wrap(apierr.CodeInternal, "failed to read command", err)
		}
		switch status {
		case "succeeded":
			return protocol.CommandResult{OK: true, Result: result}, nil
		case "failed":
			msg := ""
			if errMsg != nil {
				msg = *errMsg
			}
			return protocol.CommandResult{OK: false, Error: msg}, nil
		case "expired":
			return protocol.CommandResult{}, errors.New("command expired before the agent executed it (is the agent online?)")
		}
		select {
		case <-ctx.Done():
			return protocol.CommandResult{}, ctx.Err()
		case <-deadline.C:
			return protocol.CommandResult{}, errors.New("timed out waiting for the agent")
		case <-tick.C:
		}
	}
}

// Poll hands pending, unexpired commands to an authenticated agent exactly
// once each (marked delivered under a row lock).
func (s *Service) Poll(ctx context.Context, a Agent) ([]protocol.Command, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE node_agent_commands SET status = 'delivered', delivered_at = now()
		WHERE id IN (
			SELECT id FROM node_agent_commands WHERE agent_id = $1 AND status = 'pending' AND expires_at > now()
			ORDER BY created_at LIMIT 10 FOR UPDATE SKIP LOCKED)
		RETURNING id, agent_id, request_id, op, params, expires_at, signature`, a.ID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to poll commands", err)
	}
	defer rows.Close()
	var out []protocol.Command
	for rows.Next() {
		var c protocol.Command
		var id, aid uuid.UUID
		var exp time.Time
		var sig []byte
		if err := rows.Scan(&id, &aid, &c.RequestID, &c.Op, &c.Params, &exp, &sig); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan command", err)
		}
		c.ID, c.AgentID, c.ExpiresAt, c.Signature = id.String(), aid.String(), exp.Unix(), base64.StdEncoding.EncodeToString(sig)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReportResult records an agent's result for a delivered command.
func (s *Service) ReportResult(ctx context.Context, a Agent, cmdID uuid.UUID, res protocol.CommandResult) error {
	if len(res.Result) > 256<<10 {
		return apierr.Validation("result too large")
	}
	status := "succeeded"
	if !res.OK {
		status = "failed"
	}
	result := res.Result
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	var op string
	err := s.pool.QueryRow(ctx, `
		UPDATE node_agent_commands SET status = $3, result = $4, error = NULLIF($5, ''), completed_at = now()
		WHERE id = $1 AND agent_id = $2 AND status = 'delivered' RETURNING op`, cmdID, a.ID, status, result, truncate(res.Error, 2000)).Scan(&op)
	if errors.Is(err, pgx.ErrNoRows) {
		return apierr.Conflict("command is not awaiting a result from this agent")
	}
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to record result", err)
	}
	s.rec(ctx, s.sysCtx(a.OrganizationID, "node-agent"), "nodeagent.command.completed", "node_agent", a.ID.String(), res.OK,
		map[string]any{"op": op, "command_id": cmdID.String(), "status": status})
	return nil
}

// ---- Management ----

func (s *Service) List(ctx context.Context, ac authctx.AuthContext) ([]Agent, error) {
	if err := rbac.Require(ac, permRead); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+agentCols+` FROM node_agents a JOIN nodes n ON n.id = a.node_id WHERE a.organization_id = $1 ORDER BY n.hostname`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list agents", err)
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan agent", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) Revoke(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, permManage); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE node_agents SET status = 'revoked', revoked_at = now() WHERE id = $1 AND organization_id = $2 AND status <> 'revoked'`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke agent", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("agent")
	}
	_, _ = s.pool.Exec(ctx, `UPDATE node_agent_commands SET status = 'expired' WHERE agent_id = $1 AND status IN ('pending', 'delivered')`, id)
	s.rec(ctx, ac, "nodeagent.agent.revoked", "node_agent", id.String(), true, nil)
	return nil
}

// Sweep marks silent agents offline, expires stale commands and prunes nonces.
// Safe to run concurrently and repeatedly.
func (s *Service) Sweep(ctx context.Context) error {
	cutoff := s.now().Add(-OfflineAfter)
	rows, err := s.pool.Query(ctx, `
		UPDATE node_agents SET status = 'offline'
		WHERE status IN ('online', 'degraded') AND (last_seen_at IS NULL OR last_seen_at < $1)
		RETURNING node_id`, cutoff)
	if err != nil {
		return err
	}
	var nodes []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			nodes = append(nodes, id)
		}
	}
	rows.Close()
	for _, n := range nodes {
		_, _ = s.pool.Exec(ctx, `UPDATE nodes SET status = 'offline', updated_at = now() WHERE id = $1 AND status NOT IN ('decommissioned')`, n)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE node_agent_commands SET status = 'expired' WHERE status IN ('pending', 'delivered') AND expires_at < now()`); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM node_agent_nonces WHERE seen_at < now() - interval '10 minutes'`)
	return err
}

var _ = fmt.Sprintf
var _ = strings.TrimSpace
