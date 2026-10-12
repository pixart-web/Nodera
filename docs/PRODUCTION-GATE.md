# Production gate: HETZNER STAGING → CUSTOMER PRODUCTION

Customer workloads are **not** onboarded until every line below is satisfied **with evidence** (a link, a pasted output, a recorded run). The gate is deliberately
about the *first production architecture* (Nodera + Traefik + Docker + MariaDB + Storage Box on one node). Anything outside it is not a blocker unless a specific customer
workload needs it.

Stages (`docs/DEPLOYMENT.md`): IMPLEMENTED → LOCALLY TESTED → **STAGING VALIDATED** → **PRODUCTION VALIDATED**. Today everything is at most LOCALLY TESTED; passing this gate is what moves the items below to PRODUCTION VALIDATED.

Release under test: ____________ (tag / digests)  Staging host: ____________  Gate reviewed by: ____________  Date: ____________

## The gate

| # | Requirement | Evidence | ☐ |
|---|---|---|---|
| 1 | **GitHub CI green** on the release commit: API, database-roles, web, images jobs | link to the workflow run | ☐ |
| 2 | **All three images build** and are published immutably (`nodera-api`, `nodera-web`, `nodera-agent`) by the Release workflow; staging runs them **by digest** | release run summary + `docker ps --format '{{.Image}}'` | ☐ |
| 3 | **Production compose validated**: `docker compose config` and `scripts/check-compose-prod.py` pass, and the stack runs from it unmodified | CI log; `docker compose ps` | ☐ |
| 4 | **Real Traefik HTTPS validated**: publicly trusted certificate, HTTP→HTTPS, `/api/`+`/agent/` routing, SSE progressive (smoke A2, A3, A7, E1) | smoke record | ☐ |
| 5 | **Real PostgreSQL validated**: roles split, migration job, `post-migrate.sh` 10/10, audit `DELETE` refused, not published (smoke A4, B6, B7) | smoke record | ☐ |
| 6 | **Real Redis validated**: not published, password, restart/outage behaviour (smoke A5; disaster 4, 10) | records | ☐ |
| 7 | **Real Node Agent validated**: enrolled, heartbeat, signed commands, replay/invalid/expired/forged all refused (smoke C1–C6; disaster 16, 17) | records | ☐ |
| 8 | **Real Docker operations validated** through the agent, incl. control-plane protection and the data-root mount rule (smoke C7–C9, D2, D7) | records | ☐ |
| 9 | **Real MariaDB validated**: create, dump, restore, failure handling, redaction, large database (smoke D1–D8; disaster 6, 12) | records | ☐ |
| 10 | **Off-site backup validated** against the real Storage Box: pinned host key, upload, hash-verified, retention, wrong key refused (smoke F1–F5, F7; disaster 13) | records | ☐ |
| 11 | **Real restore validated** incl. corruption refusal and rollback after a forced failure (smoke F3, F4, F6; disaster 14, 15) | records | ☐ |
| 12 | **Nodera database DR procedure validated**: `backup-nodera.sh` → read-back `verify` → full `apply` onto a scratch host (disaster 20) with a measured recovery time | record + timeline | ☐ |
| 13 | **Key custody confirmed**: `secrets-encryption-key`, `agent-signing-key`, age identity each exist in two offline places, **not** on the server or the Storage Box; a second person can retrieve them; disaster 20 used only the custody copies (`docs/KEY-CUSTODY.md`) | signed custody checklist | ☐ |
| 14 | **Reboot recovery validated**: host reboot, everything returns without intervention (disaster 8) | record | ☐ |
| 15 | **Audit protections applied** on the real database and verified as the runtime role (smoke B6, B7) | record | ☐ |
| 16 | **Critical disaster tests passed**: 1, 3, 5, 8, 9, 13, 14, 15, 16, 17, 18, 20; the rest run and triaged (`docs/DISASTER-TESTS.md`) | the summary table, all PASS | ☐ |
| 17 | **No unresolved P0/P1 security finding**: staging smoke security phase all PASS; `npm audit` policy and `govulncheck` clean; no open finding in the review of the deployed configuration | CI + review notes | ☐ |
| 18 | **Branch protection and release policy in force** on the repository (`docs/RELEASES.md`) | screenshot / settings export | ☐ |
| 19 | **Monitoring/alerting you will actually rely on exists**: disk > 80 %, backup timer failure, `/ready` degraded, Storage Box unreachable — Nodera does not page anyone itself | where the alerts go + a test alert | ☐ |
| 20 | **Pilot workload plan agreed**: one low-risk site first, rollback plan written (previous images, last backup, how to stop the migration) | the plan | ☐ |

## Explicitly NOT required for this gate

Cloudflare automation, a generic ACME client, GitHub deployment provider / build runner / webhooks, FTP/SFTP/SSH/cPanel migration connectors, SMTP / email / password recovery / invitations,
a client portal, Hetzner Cloud provider, horizontal scaling. They are NOT IMPLEMENTED (`docs/ROADMAP.md`) and do not block a first pilot **unless a specific customer workload depends on one** — then that dependency becomes a line above.

## What passing the gate does NOT mean

It means the first architecture works on this host and its failures are understood. It does not make Nodera "complete", and it does not cover: more than one API instance, a second node, multi-region,
workloads other than WordPress/containers, or any integration marked NOT IMPLEMENTED. Re-run the relevant parts of the smoke and disaster tests after every host change, major upgrade, or new capability.

## After the gate

`FIRST PILOT WORKLOAD → CUSTOMER MIGRATION`: one site, watched for at least a week (backup success, restore drill on that site, resource use), then the rest. A failed disaster test or a data-loss incident on the pilot reopens the gate.
