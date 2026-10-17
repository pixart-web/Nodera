# NODERA — Customer Portfolio Validation

Date: 2026-10-17

## WordPress inventory

- Pixart — WordPress 7.1.3 / PHP 8.1.34; uploads approximately 358 MB.
- BabyMoura — WordPress 7.1.3 / PHP 8.3.35; uploads approximately 1 MB.
- Casa das Fardas — WordPress 7.1.3 / PHP 8.3.35; uploads approximately 750 MB.
- MotoGreen — WordPress 7.1.3 / PHP 8.3.35; uploads approximately 135 MB.
- DiversCape — WordPress 7.1.3 / PHP 8.3.35; uploads approximately 18 MB.
- Sistema Elite DP — WordPress 7.1.3 / PHP 8.3.35; uploads approximately 362 MB.

## Runtime isolation

- Each inspected WordPress workload has its own WordPress volume.
- Each inspected workload has its own MariaDB container/database boundary, except Pixart which uses the existing Nodera MariaDB service with its own database.
- No host listeners were found on 3306, 5432 or 6379.
- Customer WordPress containers use the shared reverse-proxy network while application/database networks remain separate.
- Casa das Fardas and MotoGreen were corrected to `restart: unless-stopped` in their production compose overrides. Both were recreated successfully and returned HTTP 200 afterwards.

## Public smoke evidence

HTTPS requests to Pixart, BabyMoura, Casa das Fardas, MotoGreen and DiversCape returned HTTP 200. Sistema Elite DP returned HTTP 200 when resolved to the server IP using its configured `www` hostname.

## Backup evidence

A local WordPress backup job is installed at `/srv/nodera/scripts/backup/wordpress-backup.sh`. It creates database dumps, WordPress volume archives and SHA-256 manifests for all six inspected installations.

Backup run `20261017T085435Z` completed successfully. All six manifests verified successfully. Total backup size was approximately 941 MB.

A daily 03:15 UTC cron entry is installed for the WordPress backup job.

## Remaining gates

- Off-host encrypted backup is still required.
- Isolated restore testing is still required; checksum validation alone is not restore validation.
- Pixart PHP 8.1 upgrade requires compatibility review before change.
- Forms, email, cron and external integrations require site-by-site functional verification.
- Sistema Elite DP currently exposes the configured `www` hostname; apex routing/redirect remains to be decided and implemented in a controlled domain pass.
