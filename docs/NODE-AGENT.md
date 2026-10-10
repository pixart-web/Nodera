# Node Agent

The agent runs **on each managed node**, separate from the control plane
(`api/cmd/agent`, `--target agent` image). It is a *pull* agent: it dials out to
the API and opens no port. There is no generic "execute shell" endpoint, and
there never will be.

## Lifecycle

1. An operator with `infrastructure.manage` calls
   `POST /api/v1/infrastructure/nodes/{id}/agent-registrations` → a one-time
   enrolment token (`ndr_enr_…`, 30 min, shown once, stored only as a SHA-256 hash).
2. On the node: `nodera-agent enroll --api https://… --token …` generates an
   Ed25519 key pair **locally** (the private key never leaves the node; state file mode 0600),
   exchanges the token + public key for an agent identity and the server's command-signing public key.
3. `nodera-agent run` loops: signed heartbeat → signed poll → execute → signed result.
   Heartbeats set the node `online`/`degraded` and store CPU/RAM/disk samples; a sweeper
   marks agents `offline` after 90 s of silence.
4. `POST /api/v1/infrastructure/agents/{id}/revoke` locks an agent out immediately.
   One live agent per node; a second enrolment is refused until the first is revoked.

## Wire security

* **Agent → API requests** carry `X-Nodera-Agent`, `X-Nodera-Timestamp`, `X-Nodera-Nonce`,
  `X-Nodera-Signature` = Ed25519 over `agent|timestamp|nonce|METHOD|path|sha256(body)`.
  Timestamp must be within ±60 s; every `(agent, nonce)` is stored once, so a replay is rejected.
  Body ≤ 1 MiB. Per-agent rate limit (600/min); enrolment is limited to 10/hour/IP.
* **API → agent commands** are signed by the server (canonical JSON, Ed25519) and bound to one
  agent, one request id and an expiry. The agent verifies the signature, the agent id and the
  expiry **before** anything runs.
* **Allowlist.** Only these operations exist: `docker.create|start|stop|restart|remove|inspect|logs`,
  `filesystem.read|write|mkdir|remove`, `service.health`, `metrics.collect`. Each has a parameter
  validator (container names/images are regex-checked, no leading `-`; paths must be relative with no
  `..`; health targets must be http(s)/host:port). The check runs at enqueue time on the server **and**
  again on the agent. Docker is invoked with argv arrays, never through a shell.
* Every queue/report action is audited; errors never echo secrets.

## Tested (cmd/server/agent_test.go, security_test.go, protocol_test.go)

enrolment single-use + expiry, one agent per node, heartbeat → node online, signed command round trip,
non-allowlisted ops, traversal/absolute paths, injected image/name, replay, stale timestamp, tampered body,
unsigned/garbage/wrong-agent signatures, oversized body, malformed nonce, revoked agent, cross-agent result
forgery, cross-tenant registration/enqueue/revoke/list, RBAC.

## Requires the real node

Running `--driver docker` against a real Docker daemon, TLS to the production API, and the host
Docker group id (`NODERA_DOCKER_GID`) are only verified on the actual machine.
