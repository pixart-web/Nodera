# Node Agent

The agent runs **on each managed node**, separate from the API (`api/cmd/agent`, `--target agent` image). It is a *pull* agent: it dials out to the API and
opens no port. There is no generic "execute shell" endpoint, and there never will be.

**Status:** IMPLEMENTED + LOCALLY TESTED (protocol and executor in CI; real Docker daemon and a real MariaDB container on a local Linux VM). **Real Hetzner host: REAL VALIDATION PENDING** (Docker GID, socket permissions, bind-mount ownership, reboot behaviour).

## Why it matters: it is the only component with the Docker socket

In production (`NODERA_PROVIDER_MODE=agent`) the API has **no** Docker access. Container and MariaDB operations are queued as signed, expiring, allowlisted commands that this agent executes. The agent is therefore
root-equivalent on the host. Its defences: the allowlist below, the protected-resource guard, no listening port, uid 10001, read-only root filesystem, all capabilities dropped, `no-new-privileges`.
`docs/DEPLOYMENT.md` explains the trust model and the break-glass path (SSH + Docker, independent of Nodera).

## Lifecycle

1. An operator with `infrastructure.manage` calls `POST /api/v1/infrastructure/nodes/{id}/agent-registrations` → a one-time enrolment token (`ndr_enr_…`, 30 min, shown once, stored only as a SHA-256 hash).
2. `nodera-agent enroll --api URL --token …` (in the compose stack: `docker exec nodera-cp-agent nodera-agent enroll --api http://api:8080 --token …`) generates an Ed25519 key pair **locally**
   (the private key never leaves the node; state file mode 0600), exchanges the token + public key for an agent identity and the server's command-signing public key (which it pins).
   An agent container started before enrolment waits ("not enrolled yet") instead of crash-looping.
3. `nodera-agent run` loops: signed heartbeat → signed poll → execute → signed result (every 5 s). Heartbeats set the node `online`/`degraded` and store CPU/RAM/disk samples; a sweeper marks agents `offline` after 90 s of silence.
   Network or server errors (including a database outage on the API side) are logged and **retried**; they are not treated as revocation.
4. `POST /api/v1/infrastructure/agents/{id}/revoke` locks an agent out immediately. One live agent per node; a second enrolment is refused until the first is revoked.
5. For `NODERA_PROVIDER_MODE=agent` the platform operator names the agent in `NODERA_LOCAL_AGENT_ID` (a tenant cannot enrol an agent that receives platform work). `/ready` reports `agent: ok | offline | revoked | not_configured | unknown`.

## Wire security

* **Transport:** `https://` is required, except for loopback and single-label internal service names (`http://api:8080` on the private Compose network) — `agent.ValidateAPIURL`, enforced at enrol and at run. Signatures give integrity and replay protection, not confidentiality.
* **Agent → API requests** carry `X-Nodera-Agent`, `X-Nodera-Timestamp`, `X-Nodera-Nonce`, `X-Nodera-Signature` = Ed25519 over `agent|timestamp|nonce|METHOD|path|sha256(body)`.
  Timestamp within ±60 s (keep NTP running), every `(agent, nonce)` stored once so a replay is rejected, body ≤ 1 MiB, per-agent rate limit (600/min), enrolment 10/hour/IP. A database failure on the API returns 5xx, not 401.
* **API → agent commands** are signed by the server (canonical JSON, Ed25519), bound to one agent, one request id, and a 5-minute expiry. The agent verifies signature, agent id and expiry **before** anything runs, remembers
  executed command ids until they expire (a replayed copy is refused: *"command already executed"*), and applies an execution time limit (5 min; 2 h for database dump/load).
* **Allowlist** (`protocol.Allowed`): `docker.create|start|stop|restart|remove|inspect|logs`, `filesystem.read|write|mkdir|remove`, `database.ensure|drop|exists|dump|load`, `service.health`, `metrics.collect`.
  Each has a validator (container names/images regex-checked, no leading `-`; paths relative with no `..`; database identifiers `[A-Za-z0-9_]{1,48}`, passwords `[A-Za-z0-9_-]{16,128}`; workload `user` numeric and **not root**; health targets http(s)/host:port).
  Checked at enqueue on the server **and** again on the agent. Docker and MariaDB are invoked with argv arrays, never through a shell; SQL travels on stdin; the root password only through the environment of the `docker exec` (never argv).
* **Database dump/load** do not stream through the command channel. The agent writes/reads a file under the shared data directory (`O_EXCL`, mode 0600, containment-checked) and the API streams it, so memory is bounded and the file is removed afterwards. API and agent therefore share the data directory at the **same absolute path** and the same uid.
* **Protected resources** (`internal/nodeagent/guard`): lifecycle commands on `nodera-cp-*`, anything labelled `nodera.role=control-plane`, or `NODERA_PROTECTED_CONTAINERS` (default `traefik,mariadb`) are refused; `docker.create` refuses volume sources outside the data root (so not the Docker socket, `/`, `/etc`, the Postgres volume, or named volumes), host networking, and a root user. No allowlisted operation removes volumes or networks.
* **Secrets in parameters:** `docker.create` (container environment) and `database.ensure` (password) are blanked in `node_agent_commands` as soon as the command finishes. Every queue/report action is audited; errors never echo secrets.

## Configuration (agent container)

| Variable | Meaning |
|---|---|
| `NODERA_AGENT_DRIVER` | `docker` (real) or `mock` (simulated; logs a warning) |
| `NODERA_AGENT_DATA` | data directory; same path as the API's `NODERA_DATA_DIR` in production |
| `NODERA_AGENT_STATE` | state file (`/var/lib/nodera-agent/state.json` in the image; a named volume) |
| `NODERA_MARIADB_CONTAINER`, `NODERA_MARIADB_ROOT_PASSWORD_FILE` | the existing MariaDB container and its root password file (enables the `database` capability) |
| `NODERA_MARIADB_LEGACY_CLIENT=true` | for MariaDB < 10.5 (`mysql`/`mysqldump`) |
| `NODERA_PROTECTED_CONTAINERS` | extra container names that managed operations may never touch |

**Docker socket GID:** the container user is uid 10001 and joins the host's docker group through `group_add: ["${NODERA_DOCKER_GID}"]` (`getent group docker | cut -d: -f3`). A wrong GID shows as `permission denied` on `/var/run/docker.sock` in the agent log.
The agent image bundles only the Docker CLI client (`docker-cli`), no daemon.

## Diagnostics

`nodera-agent selftest` (inside the container) sends one valid heartbeat and seven deliberately bad requests signed with the agent's own identity — replayed nonce, tampered body, stale and future timestamp, foreign key, unknown agent, unsigned — and exits non-zero unless the
bad ones are all refused. It is part of the staging smoke tests (`docs/SMOKE-TESTS.md` C4).

## Tested

CI (`cmd/server/agent_test.go`, `agent_mode_test.go`, `security_test.go`, `protocol_test.go`, `executor_test.go`, `guard_test.go`, `agent/replay_test.go`): enrolment single-use + expiry, one agent per node, heartbeat → node online, signed command round trip, non-allowlisted ops,
traversal/absolute paths, injected image/name/identifiers, replay (request and command), stale timestamp, tampered body, unsigned/garbage/wrong-agent signatures, oversized body, revoked agent, cross-agent result forgery, cross-tenant registration/enqueue/revoke/list, RBAC,
control-plane protection, mount rules, root-user refusal, database dump/load through files, secrets scrubbed from stored parameters, API-over-agent end to end (containers + database), database failure ≠ 401.
Locally with a real daemon: provisioning a WordPress project through the agent against a real MariaDB, a 27 MB backup with a database dump, restore, protected-container refusals, `selftest`.

## Requires the real node

Docker GID and socket permissions; that workload bind mounts owned by uid 10001 behave on the host filesystem; MariaDB version and container name; behaviour after reboot (`docs/DISASTER-TESTS.md` 8).
