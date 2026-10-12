# Real-environment smoke tests (Hetzner staging)

Run after `docs/HETZNER-STAGING-RUNBOOK.md`, with **test data only** — never a customer's site or database. Record PASS/FAIL, the date, the release (tag/digest), who ran it, and paste evidence
(command output, screenshot, audit entry id) next to each line. A FAIL is a finding to fix and re-run, not a line to delete.

`HOST` = `NODERA_HOST`. `smoke.py` = `python3 scripts/staging-smoke.py --base https://HOST --state ./smoke.json` (a throw-away operator; its password lives only in `smoke.json`, mode 0600).
`API` = `docker exec nodera-cp-api wget -qO- http://127.0.0.1:8080`. DB access from the host: `docker exec -i nodera-cp-postgres psql -U postgres -d nodera -tAc "…"`.

Release: ____________  Operator: ____________  Date: ____________

## A. Network

| # | Check | How | Expected | Result |
|---|---|---|---|---|
| A1 | DNS resolves | `dig +short HOST` | the server's address | |
| A2 | HTTPS works, certificate is valid and publicly trusted | `curl -sS -o /dev/null -w '%{http_code} verify=%{ssl_verify_result}\n' https://HOST/login` | `200 verify=0` (issuer Let's Encrypt, not "TRAEFIK DEFAULT CERT") | |
| A3 | HTTP redirects to HTTPS | `curl -sI http://HOST/ \| head -3` | `301`/`308` to `https://HOST/` | |
| A4 | PostgreSQL is not public | from a machine **outside** the server: `nmap -Pn -p 5432,6379,3306,8080,3000,2375,2376 HOST`; on the server: `docker ps --format '{{.Names}}\t{{.Ports}}'` | all closed/filtered; no `0.0.0.0:`/`:::` bindings except Traefik 80/443 | |
| A5 | Redis is not public | same as A4 | same | |
| A6 | Only the right containers hold the Docker socket | `docker ps -q \| xargs docker inspect --format '{{.Name}} {{range .Mounts}}{{if eq .Source "/var/run/docker.sock"}}SOCKET{{end}}{{end}}' \| grep SOCKET` | exactly `traefik` and `nodera-cp-agent`; `docker exec nodera-cp-api ls /var/run/docker.sock` fails | |
| A7 | API is reachable only through Traefik | outside: `curl -m5 http://HOST:8080/health`; `curl -sk https://HOST/health -o /dev/null -w '%{http_code}'` | connection refused; the second returns the **web** 404, not the API's JSON | |
| A8 | A customer container cannot reach the control plane | provision a test WordPress project (D-section), then `docker exec nodera-<slug> php -r 'echo @file_get_contents("http://nodera-cp-api:8080/health") ?: "UNREACHABLE";'` | `UNREACHABLE` (it is on `proxy-public`, the API is on `nodera-edge`+internal) | |
| A9 | Firewall | `ufw status verbose`; `ss -tlnp` | default deny incoming; only 22 (restricted), 80, 443 listening publicly | |
| A10 | Time is synchronised | `timedatectl` | `System clock synchronized: yes` | |

## B. Nodera

| # | Check | How | Expected | Result |
|---|---|---|---|---|
| B1 | All services healthy | `docker compose … ps` | postgres, redis, api, web, agent `healthy`; migrate `exited (0)` | |
| B2 | Readiness | `API/ready` | `status ready`, `database ok`, `redis ok`, `agent ok`, `provider.containers agent`, `provider.database agent`, `provider.backups offsite` (after runbook §10), `provider.dns local_dev`, `provider.certificates local_dev` | |
| B3 | Warnings are honest | UI Dashboard → providers panel, or `GET /api/v1/system/info` | lists in-memory DNS and development CA | |
| B4 | Login, cookie flags, CSRF, tenant isolation, bearer token | `smoke.py bootstrap` then `smoke.py security` | every line `PASS` (session `HttpOnly`+`Secure`+`SameSite=Lax`; wrong/missing CSRF → 403; scoped API token works without CSRF and is limited to its scopes; second tenant sees nothing of the first) | |
| B5 | No bearer token in browser storage | browser devtools → Application → Local/Session storage after login | no session token (only UI preferences) | |
| B6 | Audit events appear and are append-only | UI Audit page; then `PGPASSWORD=$(cat /etc/nodera/secrets/db-app-password) docker exec -e PGPASSWORD nodera-cp-postgres psql -h 127.0.0.1 -U nodera_app -d nodera -c "delete from audit_log"` | events listed; the DELETE fails with `permission denied` | |
| B7 | Database role model | `scripts/post-migrate.sh` | `database protections applied and verified (10 checks)` | |
| B8 | Platform administrator | sign in as the bootstrapped admin → Platform audit / flags pages load; a normal org owner gets 403 (covered by B4) | | |
| B9 | Rate limiting works behind the proxy, per client | 6 wrong logins from one address: the 6th is `429`; a login from a **different** address is unaffected | per-client buckets (proves `NODERA_TRUSTED_PROXIES` is right; if everyone is throttled together it is wrong) | |
| B10 | Oversized body refused | `head -c 3000000 /dev/zero \| tr '\0' x \| curl -s -o /dev/null -w '%{http_code}' -X POST https://HOST/api/v1/auth/login -H 'content-type: application/json' --data-binary @-` | `400`/`413`, API memory unchanged | |

## C. Node Agent

| # | Check | How | Expected | Result |
|---|---|---|---|---|
| C1 | Enrolled | UI Infrastructure → Agents; `docker exec nodera-cp-agent cat /var/lib/nodera-agent/state.json \| head -c 0; ls -l /var/lib/nodera-agent/state.json` (do not print it) | agent `online`, state file mode `-rw-------` | |
| C2 | Heartbeat / last seen | `GET /api/v1/infrastructure/agents` | `last_seen_at` < 30 s ago, capabilities `docker`, `filesystem`, `metrics`, `database` | |
| C3 | Signed command executes | UI/API: queue `docker.inspect` `{"name":"traefik"}` on the agent; then `psql … "select op,status,left(result::text,120) from node_agent_commands order by created_at desc limit 1"` | `succeeded`, `found: true` | |
| C4 | Replay, tampering, stale/future timestamp, foreign key, unknown agent, unsigned | `docker exec nodera-cp-agent nodera-agent selftest` | 8 × `PASS` (1 accepted, 7 refused with 401) | |
| C5 | Invalid command rejected at enqueue | queue op `shell.exec`, then `docker.create` with image `-v /:/host`, then `docker.stop` with name `$(id)` | each `400` validation error; nothing in `node_agent_commands` | |
| C6 | Expired command is never executed | `docker stop nodera-cp-agent`; queue `docker.inspect traefik`; wait 6 minutes; `docker start nodera-cp-agent` | row ends `expired`, agent log has no execution | |
| C7 | Control-plane protection | queue `docker.stop` `{"name":"nodera-cp-api"}` (rejected at enqueue, `400`), `docker.remove` `{"name":"traefik"}` (accepted, then `failed: refused: protected control-plane resource`), `docker.create` with volume source `/var/run/docker.sock` (`failed: … outside the data root`) | all refused; `docker ps` unchanged | |
| C8 | Docker operations against a safe test container | queue in order: `docker.create` `{"name":"nodera-smoke-whoami","image":"traefik/whoami:v1.10","user":"10001:10001"}`, then with `{"name":"nodera-smoke-whoami"}`: `docker.start`, `docker.inspect`, `docker.logs`, `docker.stop`, `docker.remove` | each `succeeded`; container appears and disappears in `docker ps -a`; the run shows in the audit log | |
| C9 | Docker GID / socket permission | agent log shows no `permission denied` on `/var/run/docker.sock` | | |
| C10 | Command params are scrubbed | `psql … "select params from node_agent_commands where op in ('docker.create','database.ensure') order by created_at desc limit 3"` | `{"redacted": true}` once finished | |

## D. MariaDB (the real container, test databases only)

| # | Check | How | Expected | Result |
|---|---|---|---|---|
| D1 | Connect / version | `docker exec mariadb mariadb --version`; agent capability list contains `database` | | |
| D2 | Create | `smoke.py provision` (creates a TEST WordPress project: database + user + container) | 8 steps `succeeded`; `docker exec mariadb sh -c 'mariadb -uroot -p"$MARIADB_ROOT_PASSWORD" -e "show databases"'` lists `wp_smoke_…` | |
| D3 | Dump | UI/API: create a `database` backup of the test project | `completed`, checksum recorded, archive on the Storage Box | |
| D4 | Restore | add a row/table to the test DB, restore the backup, row gone | restore `succeeded` after approval; safety snapshot taken | |
| D5 | Failure handling | stop MariaDB (`docker stop mariadb`) and create a database backup | operation `failed` with a clear error, no `completed` record, no temp files in `/srv/nodera/data/backups` or `.tmp-db`; `docker start mariadb` and re-run succeeds | |
| D6 | Credential redaction | `docker logs nodera-cp-api nodera-cp-agent 2>&1 \| grep -c "<the MariaDB root password>"`; same for the generated project DB password | `0` for both | |
| D7 | Workload file ownership | `ls -ln /srv/nodera/data/fs/projects/<slug>/data \| head` | owner `10001`; a **restore** of the file backup succeeds (no "permission denied") | |
| D8 | Large database | create ~1 GB of test data in a scratch database, back it up | completes; `docker stats` shows API/agent memory flat; disk use as predicted | |

## E. Operations

| # | Check | How | Expected | Result |
|---|---|---|---|---|
| E1 | Progress over SSE | open an operation in the UI while it runs; or `curl -N --cookie … https://HOST/api/v1/operations/<id>/stream` | events arrive **as the steps happen** (not all at the end) — i.e. Traefik is not buffering | |
| E2 | Cancellation | start a backup of a larger site, cancel it | status `cancelled`, no partial archive remote or local, `.tmp-backup-*` removed | |
| E3 | Failure + rollback | provision with MariaDB stopped | `failed`, completed steps rolled back in reverse order, project not left `provisioning` | |
| E4 | Idempotency | submit the same `Idempotency-Key` twice | the second returns the first job (`"created": false`) | |
| E5 | Approval | restore a backup | `approval_required`; nothing runs until a human approves | |
| E6 | Duplicate approval | decide the same approval twice | second call `409 approval is no longer pending` | |

## F. Backup (workloads, off-site)

| # | Check | How | Expected | Result |
|---|---|---|---|---|
| F1 | Create → off-site | full backup of the test project | `completed`; `<id>.tar.gz` **and** `<id>.tar.gz.sha256` on the Storage Box; staging copy removed | |
| F2 | Checksum | `sftp` the object back, `sha256sum` it | equals the backup record's `checksum_sha256` | |
| F3 | Download + restore | restore to the same project | `succeeded` (verify-source → safety-snapshot → restore → finalize) | |
| F4 | Corruption detection | flip one byte of the remote archive (`printf Z \| dd of=… bs=1 seek=1000000 conv=notrunc`), run **Verify** | backup marked `corrupt`; restore refuses ("checksum mismatch") and changes nothing | |
| F5 | Retention / delete | delete the backup (approval) | remote archive and `.sha256` gone | |
| F6 | Rollback after a forced restore failure | make the destination unwritable mid-restore (e.g. `chattr +i` on one file, as root) and restore | operation ends with the safety snapshot **put back** (`restore` step `rolled_back`), site intact; remove the flag afterwards | |
| F7 | Wrong host key refused | temporarily set a wrong `NODERA_BACKUP_SFTP_HOST_KEY_SHA256`, restart the API, create a backup | `failed`, error names the presented fingerprint, nothing uploaded; restore the correct pin | |
| F8 | Control-plane backup | `scripts/backup-nodera.sh` then (on the workstation) `scripts/restore-nodera.sh verify …` | `off-site copy verified`; `verify OK` | |

## Sign-off

All lines PASS, evidence attached: ☐  Findings opened for every FAIL: ☐  Re-run after fixes: ☐
