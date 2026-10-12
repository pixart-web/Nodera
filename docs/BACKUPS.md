# Backups and restore

Two separate things are backed up, by two separate mechanisms:

| What | How | Where it lives |
|---|---|---|
| **Customer workloads** (WordPress files, media, database dumps, configuration) | Nodera's backup engine (this page, §1–§4) | local staging → **off-site SFTP** (Hetzner Storage Box) |
| **Nodera itself** (its PostgreSQL database, deployment config) | `scripts/backup-nodera.sh` / `restore-nodera.sh` (§5) | age-encrypted package → off-site SFTP |

| Component | Status |
|---|---|
| Streaming archive/restore, checksums, safety snapshot, rollback, retention | **IMPLEMENTED + LOCALLY TESTED** |
| SFTP off-site backend (pinned host key, hash-verified) | **IMPLEMENTED — REAL HETZNER STORAGE BOX VALIDATION PENDING** (tested against an in-process SSH server in CI and a real OpenSSH SFTP server locally) |
| Control-plane DR scripts | **IMPLEMENTED + LOCALLY TESTED** (real Postgres in a container, real `sftp`, real `age`); never run on the Hetzner host |
| S3 / other destinations | NOT IMPLEMENTED (schema allows `s3`; no adapter) |

## 1. Types and operations

* **Types:** `database`, `files`, `media`, `full`, `configuration`. Archives are tar.gz with a SHA-256 checksum.
* **Create** (`backup.create`) and **verify** (`backup.verify`) are operations (live progress, cancellable, idempotent). Create always verifies right after
  writing: an unverified archive is not a backup (the record stays `failed`, nothing "completed" is created). A tampered archive is marked `corrupt` and can never be restored.
* **Restore** (`backup.restore`) needs approval (Tool Gateway; a duplicate approval attempt gets `409`). Steps: **verify source → safety snapshot → restore → finalize**.
  If the restore fails — even halfway — the safety snapshot is put back automatically; if *that* fails the operation ends `rollback_failed` and names the safety backup id for manual recovery.
* **Delete** (`backup.delete`) needs approval and refuses while a restore is using the backup.
* **Policies:** `daily|weekly|monthly` × type with `retention_days`. The scheduler submits one `backup.create` per policy per period (idempotency key = policy + period).
* **Retention:** a sweeper deletes the artifact first (local or remote), then marks the row `deleted`; a failed delete is retried next sweep.

## 2. Streaming and bounded memory

```
filesystem walk ─▶ tar ─▶ gzip ─▶ ┬─▶ temp file ─▶ fsync ─▶ atomic rename to <id>.tar.gz
                                  └─▶ SHA-256 (incremental)
database dump ───▶ temp file (a tar entry needs its size) ─▶ tar
```

* Nothing is held in RAM beyond copy buffers. Test: a **384 MiB** logical dump is backed up, verified and restored with a peak heap of **~1 MiB**
  (`TestBackupMemoryStaysBoundedForLargeInput` asserts < 96 MiB; it uses generated data, not a fixture).
* Restore streams each file through a temp file + rename in the destination directory (`WriteStream`) and pipes the database entry straight into
  the loader. Per-entry extraction is capped (64 GiB) as a decompression-bomb guard; only regular files are extracted (links and devices in an archive are ignored); path traversal in entry names is rejected.
* Temp files (`.tmp-backup-*`) are removed on success, on failure and on cancellation; leftovers from a crash (kill -9, power loss) are swept at start-up.
* A failed or cancelled run leaves **no** artifact under the final name: the archive only appears via an atomic rename once it is complete and synced.
  A file that changes size while being read fails the backup rather than producing a corrupt entry.
* **Disk, not memory, is the constraint:** keep free space ≥ the largest uncompressed database dump + the largest compressed archive under `NODERA_DATA_DIR/backups`.
* Restore **overwrites** files present in the archive; it does not delete files that are not in it.

## 3. Off-site backend: SFTP (Hetzner Storage Box)

**Why SFTP, not S3:** a Hetzner Storage Box speaks SFTP/SSH (port 23) and has no S3 API; SFTP needs no extra service, and pinning an SSH host key is a
simpler, harder-to-misconfigure trust model than TLS to an endpoint we do not control. S3-compatible storage can be added behind the same `offsite.Store` interface later.

Create → the local streaming provider builds the archive → `offsite.Backups` uploads `<id>.tar.gz` (as `.part`, then renamed) and `<id>.tar.gz.sha256` →
the remote size must match → the engine then **re-reads the remote copy and verifies its SHA-256** (this is the `Verify` call that follows every create).
Only then is the local staging copy removed (`NODERA_BACKUP_KEEP_LOCAL=true` keeps it). If the upload fails or is cancelled, the remote partials and the
local archive are deleted, the error is returned, and the backup record is `failed`. Restore of an off-site backup **downloads to a temp file while hashing, compares with the
recorded checksum, and only then extracts** — a corrupt or tampered remote object never reaches the destination filesystem. Refs: `sftp://<id>.tar.gz`; pre-existing `local://` backups
keep working after switching the destination.

Configuration (`deploy/nodera.env.example`; credentials are **files**, never environment values or JSON):

```
NODERA_BACKUP_DESTINATION=sftp
NODERA_BACKUP_SFTP_ADDR=uXXXXXX.your-storagebox.de:23
NODERA_BACKUP_SFTP_USER=uXXXXXX
NODERA_BACKUP_SFTP_DIR=nodera-backups                     # must already exist on the Storage Box
NODERA_BACKUP_SFTP_HOST_KEY_SHA256=ssh-ed25519 SHA256:…   # REQUIRED; comma-separate several
NODERA_BACKUP_SFTP_KEY_FILE=/run/secrets/backup-sftp-key  # or NODERA_BACKUP_SFTP_PASSWORD_FILE (exactly one)
```

* **Host key pinning is mandatory.** There is no known-hosts auto-add and no insecure mode; a mismatch aborts the handshake before authentication and the error names the
  presented fingerprint so you can compare. Get the pin with `ssh-keyscan -p 23 HOST | ssh-keygen -lf -` over a trusted path. A server offers several key types; binding the pin to
  one (`ssh-ed25519 SHA256:…`) also makes the client negotiate that type — an unbound `SHA256:…` only matches if the server happens to present that key.
* Private keys must be unencrypted OpenSSH/PEM keys (no passphrase). Credentials never appear in logs, errors (authentication failures are normalised to "authentication failed"),
  audit entries or HTTP responses; tests assert the password is absent from error strings.
* Every transfer honours context cancellation and has a dial timeout; one SFTP session per operation.
* The `capabilities` map reports `backups: offsite` only when this is configured; otherwise `local`, and `/api/v1/system/info` warns that backups share the server's fate.
* **Retention on the Storage Box** is Nodera's (per-policy `retention_days`), not the Box's. Enable the Storage Box's own snapshots as defence in depth.

## 4. What you must test on the real host

Real Storage Box: host-key pin, port 23, key auth, upload speed for a realistic site, `ls` of the target directory by the Box account's permissions; then a full
create → verify → corrupt a copy → verify-fails → restore → forced restore failure → rollback cycle (`docs/SMOKE-TESTS.md`).
Files written by a restore must be owned by uid 10001 (the API's uid) and workloads run as that uid (`NODERA_WORKLOAD_USER`) — a WordPress container left on its image default
(`www-data`, uid 33) owns files the API can no longer overwrite, and restore then fails with "permission denied" (found and fixed during local testing; keep the setting).

## 5. Backing up Nodera itself (control plane)

`scripts/backup-nodera.sh` (run from cron or a systemd timer **and before every release**):

1. `pg_dump -Fc` of the `nodera` database through the Postgres container (local socket; no password on the command line); the dump is listed with `pg_restore --list` to prove it is readable.
2. A `manifest.json` (UTC time, schema version, image refs, dump SHA-256) and the **non-secret** deployment config (`docker-compose.prod.yml`, `nodera.env`).
3. Everything is tar'd and **encrypted with `age` to a public key** (`NODERA_DR_AGE_RECIPIENT`). The host holds only the public half: it can write backups but cannot read them back.
4. Uploaded over SFTP with a pinned `known_hosts` (`StrictHostKeyChecking=yes`, `BatchMode`), **downloaded again and compared by SHA-256**, then retention (`NODERA_DR_KEEP`, minimum 3) prunes the oldest.
   Two newest copies are kept locally in `/var/backups/nodera` (0700). With no SFTP target it still works but prints that the package is **not** a disaster-recovery copy.

**Deliberately not in the package:** `secrets-encryption-key`, `agent-signing-key`, database passwords, the SFTP key, the age identity. They are in operator custody (`docs/KEY-CUSTODY.md`).
A database backup without them restores the data but not the ability to decrypt its secrets — so custody is part of the backup.

`scripts/restore-nodera.sh verify <pkg> <identity>` decrypts, checks the manifest and dump checksum and lists the dump; it changes nothing (run it on a schedule — a backup never read back is a hope).
`apply` is destructive: after a 10-second pause it drops and recreates `nodera`, re-runs `01-roles.sql` with the passwords from the secrets files, `pg_restore`s as `nodera_owner`, and re-applies `02-post-migrate.sql`.
Then `docker compose up -d` (the migrate job runs first), check `/ready` and the audit log. Verified locally: a row inserted after the backup was gone after `apply`, role protections were back (10/10 checks), and the stack came up healthy.
Restoring a package from an *older* schema version onto a *newer* release is handled by the migrate job on the next `up`.

Total loss of the server: provision a new host per the runbook, restore custody keys, `init-secrets.sh` (then overwrite the two custody keys), start Postgres only, `restore-nodera.sh apply`, `docker compose up -d`, re-enrol/confirm the agent.
`docs/DISASTER-TESTS.md` test 20 is the dress rehearsal and **must pass before customer workloads**.

## Tested

Local provider: round trip, rollback to the pre-restore state, corruption detection, scheduling idempotency, retention, tenancy/RBAC, streaming memory bound, failed/cancelled cleanup, stale-temp sweep, traversal.
Off-site (in-process SSH+SFTP server): upload/verify/restore/delete, corrupted remote archive detected and never restored, wrong/missing host key rejected before upload, key and password auth,
no credential in errors, failed upload leaves nothing, cancellation, object-name validation, `local://` refs still valid. Locally on real Docker: full backup with MariaDB dump through the agent (27 MB) →
SFTP → verify → restore with safety snapshot; remote corruption → `corrupt`, restore refused.
