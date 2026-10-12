# Disaster test matrix (Hetzner staging)

Run on the **staging** install, with **test workloads only**, **before** any customer workload is onboarded. Each test breaks one thing on purpose and checks that
Nodera fails *safely* (no corruption, no lost audit, no false success) and recovers as documented. A PASS means the documented behaviour was observed, with evidence.

* Take a control-plane backup first: `sudo scripts/backup-nodera.sh`.
* Keep a test WordPress project (`smoke.py provision`) and one completed off-site backup of it.
* Evidence = the log/audit/DB output named in each row. Paste it in the run record.
* "Expected" below is what the code is designed to do. Items marked **(observed locally)** were also seen on a local Linux VM with the production images; they still need to be seen on the real host.

Aliases: `ready` = `docker exec nodera-cp-api wget -qO- http://127.0.0.1:8080/ready` (prints nothing on 503: add `-S`, or use `|| echo UNAVAILABLE`).
`dcp` = `docker compose --env-file /etc/nodera/nodera.env -f /opt/nodera/docker-compose.prod.yml`. Release: ________  Date: ________  Operator: ________

---

## 1. Restart the Nodera API
**Action:** `docker restart nodera-cp-api` — once idle, once while a backup operation is running (start a full backup, restart ~2 s in).
**Expected:** UI/API unavailable ~10–30 s (Traefik returns 404/502 until the healthcheck passes), then back; sessions survive (they live in Postgres); the browser's SSE streams reconnect and resume from `Last-Event-ID`; the agent logs failed heartbeats and recovers by itself.
The in-flight operation is marked **`failed`** at start-up with *"interrupted: the control plane restarted…"* and its running step `failed`; it does not stay `running`. `POST /api/v1/jobs/<id>/retry` re-runs it (steps are idempotent).
**Data that may be lost:** the in-flight operation's progress only; its partial output is cleaned (temp files swept at start-up; no remote partial counts as a backup).
**Automatic recovery:** `restart: unless-stopped`, healthcheck, start-up recovery of interrupted jobs. **Manual:** retry the failed operation.
**Evidence:** `docker logs nodera-cp-api` (`jobs interrupted by the previous shutdown were marked failed`), the operation's steps, the audit log. **Result:** PASS / FAIL ____

## 2. Restart the Web UI
**Action:** `docker restart nodera-cp-web`. **Expected:** UI returns 404/502 for a few seconds; API, agent and all operations unaffected; no data impact (stateless). **Lost:** nothing. **Automatic:** restart policy. **Manual:** none. **Evidence:** `curl` loop on `/login`; operations continue. **Result:** PASS / FAIL ____

## 3. Restart the Node Agent
**Action:** `docker restart nodera-cp-agent`, also while a provisioning operation is waiting on a command.
**Expected:** `/ready` shows `agent: offline` only if it stays down > 90 s; heartbeats resume within ~10 s of start. A command still `pending` is delivered after the restart if it has not expired (5 min). A command already `delivered` but not reported is **not** re-delivered: it ends `expired`, the operation waiting for it fails after 2 min and rolls back; retry the operation. The agent refuses a second copy of a command it already ran (in-memory set until expiry).
**Lost:** the in-flight command's effect may have happened or not — operations re-check state (idempotent steps). **Automatic:** restart policy. **Manual:** retry the operation. **Evidence:** `psql … "select op,status,error from node_agent_commands order by created_at desc limit 5"`, agent log, `ready`. **Result:** PASS / FAIL ____

## 4. Restart Redis
**Action:** `docker restart nodera-cp-redis`. **Expected:** no outage. `/ready` shows `redis: down`, `status: degraded`, then `ok` again **(observed locally)**. Rate limiters degrade to per-instance limits meanwhile (login stays limited at 5/5 min). **Lost:** the rate-limit counters (acceptable — they are ephemeral). **Automatic:** restart policy; the Redis client reconnects. **Manual:** none. **Evidence:** `ready` before/during/after; API log "redis unavailable, using the per-instance fallback". **Result:** PASS / FAIL ____

## 5. Restart PostgreSQL
**Action:** `docker restart nodera-cp-postgres` (idle), and once while an operation is running.
**Expected:** `/ready` → 503 `database unreachable` while down; API requests return 5xx (never a success); `/health` stays 200; the UI keeps loading but shows errors; connection pool reconnects **without restarting the API** **(observed locally: ready again ~1 s after Postgres is healthy)**. Agent heartbeats get 5xx and retry. Customer sites keep running (they do not depend on Nodera).
An operation that was mid-step when the database vanished may fail or be left `running` (its status writes failed while the process stayed alive): **restart the API** — start-up recovery marks it `failed`, then retry it.
**Lost:** requests during the outage; no committed data. **Automatic:** restart policy, pool reconnect. **Manual:** restart the API if any operation looks stuck. **Evidence:** `ready`, `docker logs`, `select id,status from jobs where status='running'`. **Result:** PASS / FAIL ____

## 6. Restart MariaDB
**Action:** `docker restart mariadb`. **Expected:** managed sites lose their DB connection briefly (WordPress shows its "error establishing a database connection" until MariaDB is back); InnoDB recovers itself; Nodera operations that need the database fail with a clear error during the gap and succeed after. **Lost:** uncommitted transactions only. **Automatic:** `restart: unless-stopped` on the MariaDB container (verify it is set). **Manual:** none. **Evidence:** site HTTP status, `docker logs mariadb`, a backup run afterwards. **Result:** PASS / FAIL ____

## 7. Restart Traefik
**Action:** `docker restart traefik`. **Expected:** all HTTPS endpoints down for a few seconds; certificates persist in the ACME volume (no re-issue — check there is no new Let's Encrypt order); Nodera, agent (internal network) and workloads keep running; UI/SSE reconnect. **Lost:** in-flight HTTP requests. **Automatic:** restart policy. **Manual:** none. **Evidence:** `curl -sI https://HOST/login` loop, Traefik log. **Result:** PASS / FAIL ____

## 8. Reboot the Hetzner host
**Action:** `reboot` (with the test project and a completed backup in place).
**Expected:** Docker starts at boot; every container comes back (`restart: unless-stopped`; Traefik and `mariadb` too). Docker does not honour `depends_on` at boot, so the API may start before Postgres is ready, exit, and be restarted by Docker until Postgres is healthy — it ends `healthy` within ~1–2 min. `migrate` (`restart: "no"`) stays exited. `/ready` ok, agent `online`, sites up, scheduled DR timer still enabled. No manual step.
**Lost:** in-flight operations (marked failed at API start, §1); Redis counters. **Automatic:** all of it. **Manual:** retry interrupted operations. **Evidence:** `uptime`, `docker ps`, `ready`, `systemctl list-timers nodera-backup.timer`, time-to-healthy. **Result:** PASS / FAIL ____

## 9. Agent becomes unreachable
**Action:** `docker stop nodera-cp-agent`; wait 2 min; then try (a) a files-only backup, (b) a database backup, (c) provisioning; then `docker start`.
**Expected:** after 90 s `/ready` shows `agent: offline`, status `degraded`, with a plain explanation; the node shows offline. (b) and (c) fail after the command wait (2 min) with *"timed out waiting for the agent"* / *"command expired"*, roll back cleanly and leave no partial resources; (a) succeeds (no agent needed). Nothing queued executes late: a command that outlived its 5-minute TTL becomes `expired` and is never run after the agent returns (smoke C6).
**Lost:** nothing. **Automatic:** reconnect on start. **Manual:** retry the failed operations. **Evidence:** `ready`, operation errors, `node_agent_commands.status`. **Result:** PASS / FAIL ____

## 10. Redis unavailable during a rate-limited request
**Action:** `docker stop nodera-cp-redis`; send 6 wrong logins from one address, and 1 login from another address; `docker start nodera-cp-redis`.
**Expected:** the 6th wrong login is `429` even with Redis down (per-instance fallback, not fail-open); the other address is unaffected; after Redis returns the shared limiter takes over. **Lost:** counters. **Automatic:** yes. **Manual:** none. **Evidence:** response codes, API log. **Result:** PASS / FAIL ____

## 11. PostgreSQL unavailable (extended outage)
**Action:** `docker stop nodera-cp-postgres`; wait 3 min; use the UI and the API; check a customer site; `docker start`.
**Expected:** as §5, for longer: no panic, no hang (requests time out or fail fast), no success reported for anything that was not persisted; the agent keeps retrying (and treats server errors as retryable, **not** as revoked credentials); workloads untouched. After start: everything recovers without restarting the API. **Lost:** requests during the outage. **Manual:** restart the API only if an operation stayed `running`. **Evidence:** `ready` timeline, agent log (5xx, not 401), site HTTP status. **Result:** PASS / FAIL ____

## 12. MariaDB unavailable during an operation
**Action:** start a database backup (or provisioning) and `docker stop mariadb` while it runs.
**Expected:** the operation fails with the client's error (credentials never in it), is rolled back step by step, and leaves **no** `completed` backup record, no archive (local or remote) and no `.tmp-*` files; a provisioning attempt leaves no half-created database/container/project state. Re-running after `docker start mariadb` succeeds. **Lost:** the run. **Automatic:** rollback. **Manual:** re-run. **Evidence:** operation steps/logs, `ls /srv/nodera/data/backups /srv/nodera/data/.tmp-db`, Storage Box listing. **Result:** PASS / FAIL ____

## 13. Off-site backup destination unavailable
**Action:** block the Storage Box (`iptables -I DOCKER-USER -d <box-ip> -j DROP`, remove afterwards) or set a wrong host/port; create a workload backup and run `scripts/backup-nodera.sh`.
**Expected:** the workload backup **fails** (`off-site upload failed: …`), the local staging copy is deleted, the record is `failed` — never `completed`; nothing remote is left behind; with the destination back, a re-run succeeds. `backup-nodera.sh` exits non-zero and keeps its local package (the 2 newest), printing that it is not an off-site copy. The scheduler does not silently report success: a failed policy period is visible as a failed backup and is retried at the next period (or manually now).
**Lost:** that run. **Automatic:** none beyond the next scheduled run. **Manual:** re-run; alert on `journalctl -u nodera-backup` failures (not built in). **Evidence:** operation error, Storage Box listing, script exit code. **Result:** PASS / FAIL ____

## 14. Backup corruption
**Action:** flip one byte in a remote archive; run **Verify**; then try **Restore**. (Smoke F4.) Also corrupt a control-plane package and run `restore-nodera.sh verify`.
**Expected:** the backup becomes `corrupt` ("checksum mismatch"); restore refuses before touching anything (the download is hashed against the recorded checksum before extraction); the control-plane package fails `age` authentication or the manifest checksum. **Lost:** that backup. **Manual:** restore from an earlier one. **Evidence:** backup status, operation error, destination unchanged. **Result:** PASS / FAIL ____

## 15. Restore fails after the safety snapshot
**Action:** make the restore fail midway (e.g. `chattr +i` on a file inside the site as root). (Smoke F6.)
**Expected:** steps `verify-source-backup ✓`, `safety-snapshot ✓`, `restore ✗` → the safety snapshot is **put back** (`restore` step `rolled_back`), the site equals its pre-restore state, the operation fails with a clear error. If the rollback itself fails the operation ends `rollback_failed` and names the safety backup id. **Lost:** nothing if rollback succeeds. **Manual:** remove the flag, retry. **Evidence:** steps, file listing before/after, safety backup row. **Result:** PASS / FAIL ____

## 16. Invalid agent signature
**Action:** `docker exec nodera-cp-agent nodera-agent selftest`. **Expected:** 1 accepted heartbeat, then 7 refusals (replayed nonce, tampered body, stale and future timestamp, foreign key, unknown agent, unsigned) — all `401`, each logged `agent request rejected`/`replay rejected` on the API side. **Lost:** nothing. **Evidence:** selftest output + API log. **Result:** PASS / FAIL ____

## 17. Replayed agent command
**Action:** (a) re-queue effect: after a command has `succeeded`, set its row back to `pending` before expiry (`update node_agent_commands set status='pending' where id=… and expires_at > now()`) and let the agent poll; (b) the selftest replay of §16.
**Expected:** (a) the agent refuses with *"command already executed (replay)"* and the API records the command `failed` with that error — the Docker effect does not happen twice; after expiry it is refused as expired anyway. (b) as §16. **Lost:** nothing. **Evidence:** `node_agent_commands.error`, agent log. **Result:** PASS / FAIL ____

## 18. Duplicate approval race
**Action:** `smoke.py approval-race` (8 simultaneous approvals of one restore). **Expected:** exactly one `200`, seven `409`; exactly one restore operation runs **(observed locally)**. Also: approving an already-decided/expired approval → `409`. **Lost:** nothing. **Evidence:** script output, operations list, audit. **Result:** PASS / FAIL ____

## 19. Low disk
**Action (safe, scoped):** mount a small tmpfs **only** over the backup staging directory — not the root filesystem or Docker's: `mount -t tmpfs -o size=10m,uid=10001,gid=10001,mode=0750 tmpfs /srv/nodera/data/backups && docker restart nodera-cp-api`, run a full backup of the test site, then `umount` and restart the API.
**Expected:** the backup **fails** with `no space left on device`, **0 leftover files** in the staging directory, no `completed` record, no remote object **(observed locally)**; Postgres, the UI and other operations are unaffected. **Also verify** the alerting you will rely on for the real disk (80 % threshold) exists — Nodera shows node disk % from agent heartbeats but does not page anyone. **Lost:** the run. **Manual:** free space, re-run. **Evidence:** operation error, `ls`, `df`. **Do not** fill the root filesystem or `/var/lib/docker`: it can corrupt Postgres. **Result:** PASS / FAIL ____

## 20. Total loss of the server (full restore)
**Action:** on a **scratch** Hetzner server (or a rebuilt staging server), simulate losing everything: you have only (1) operator custody (secrets-encryption-key, agent-signing-key, age identity), (2) the Storage Box. Follow `docs/BACKUPS.md` §5 and `docs/HETZNER-STAGING-RUNBOOK.md` §1–§7:
host prep → Docker/Traefik/MariaDB → release checkout → `init-secrets.sh` then **overwrite** `secrets-encryption-key` and `agent-signing-key` from custody → fetch the latest `nodera-dr-*.tar.age` → `docker compose up -d postgres` → `scripts/restore-nodera.sh apply <pkg> <identity>` → `docker compose up -d` → confirm agent → restore the test project from its off-site backup.
**Expected:** Nodera comes back with its organisations, projects, audit history and **decryptable secrets** (proves the key custody); the agent reconnects (same signing key) or is re-enrolled; the test site is restored from the Storage Box with files and database intact. Record the **total time** and every step that needed guesswork — fix the docs, not the memory.
**Lost:** everything since the last control-plane package (RPO ≤ one day with the daily timer) and since the last workload backup. **Manual:** all of it. **Evidence:** timeline, `restore-nodera.sh` output, `ready`, a decrypted secret used successfully (e.g. a connectivity check), the restored site. **Result:** PASS / FAIL ____

---

## Summary

| # | Test | Result | Evidence ref |
|---|---|---|---|
| 1–20 | as above | | |

Critical for the production gate: **1, 3, 5, 8, 9, 13, 14, 15, 16, 17, 18, 20**. The others must be run and recorded, and any FAIL triaged, but do not by themselves block the gate if the failure mode is understood and documented.
