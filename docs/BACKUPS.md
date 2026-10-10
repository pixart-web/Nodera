# Backups and restore

* **Types:** `database`, `files`, `media`, `full`, `configuration`. Archives are tar.gz with a SHA-256 checksum; the provider verifies
  every archive right after writing it — an unverified archive is not a backup (status stays `failed`).
* **Create** (`backup.create`) and **verify** (`backup.verify`) are operations. A tampered archive is marked `corrupt` and can never be restored.
* **Restore** (`backup.restore`) needs approval (Tool Gateway). Steps: verify source → **safety snapshot** → restore → finalize.
  If the restore fails (even halfway through), the safety snapshot is put back automatically. If *that* fails the error names the
  safety backup id for manual recovery.
* **Delete** (`backup.delete`) needs approval and refuses while a restore is using the backup.
* **Policies:** `daily|weekly|monthly` × type with `retention_days`. The scheduler submits one `backup.create` per policy per period
  (idempotency key = policy + period), so restarts or several API instances cannot double-run a period.
* **Retention:** a sweeper deletes the artifact first, then marks the row `deleted` (a failed delete is retried next sweep).
* **Targets:** local filesystem provider today. `s3`/`sftp` targets exist in the schema; their adapters are **not implemented** and need external credentials.

Tested with the real tar.gz provider: round trip, rollback to the pre-restore state, corruption detection, scheduling idempotency, retention, tenancy/RBAC.
