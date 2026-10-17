#!/usr/bin/env bash
# Restore the Nodera control plane database from a backup-nodera.sh package.
#
#   restore-nodera.sh verify <package.tar.age> <age-identity-file>
#       Decrypt, check the manifest and dump checksum, and LIST the dump. Changes
#       nothing. Run this regularly: a backup that has never been read back is
#       a hope, not a backup.
#
#   restore-nodera.sh apply  <package.tar.age> <age-identity-file>
#       DESTRUCTIVE. Replaces the `nodera` database in the running Postgres
#       container. Stop api/web/agent first (docker compose stop api web agent).
#       Requires the secrets files (db-owner-password, db-app-password) to exist:
#       roles are (re)created from them, then schema + data are loaded and the
#       post-migrate protections re-applied.
#
# The age IDENTITY (private key) comes from operator custody, never from this host.
set -euo pipefail
umask 077
MODE="${1:?usage: $0 verify|apply <package.tar.age> <age-identity-file>}"
PKG="${2:?package}"; ID="${3:?age identity file}"
PG="${NODERA_DR_PG_CONTAINER:-nodera-cp-postgres}"
SECRETS="${NODERA_SECRETS_DIR:-/etc/nodera/secrets}"
HERE="$(cd "$(dirname "$0")" && pwd)"
SHA="sha256sum"; command -v sha256sum >/dev/null || SHA="shasum -a 256"
command -v age >/dev/null || { echo "age is required" >&2; exit 1; }

WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
age -d -i "$ID" "$PKG" | tar -C "$WORK" -xf -
[ -s "$WORK/nodera.dump" ] && [ -s "$WORK/manifest.json" ] || { echo "package is incomplete" >&2; exit 1; }
WANT="$(sed -n 's/.*"dump_sha256":"\([0-9a-f]*\)".*/\1/p' "$WORK/manifest.json")"
GOT="$($SHA "$WORK/nodera.dump" | cut -d' ' -f1)"
[ -n "$WANT" ] && [ "$WANT" = "$GOT" ] || { echo "DUMP CHECKSUM MISMATCH — package is corrupt or tampered with" >&2; exit 2; }
echo "manifest: $(cat "$WORK/manifest.json")"
echo "dump checksum OK"
# Listing needs a pg_restore only; verify can run on a workstation that has no Nodera containers.
if command -v pg_restore >/dev/null; then pg_restore --list "$WORK/nodera.dump" | sed -n "1,5p"
elif docker inspect "$PG" >/dev/null 2>&1; then docker exec -i "$PG" pg_restore --list < "$WORK/nodera.dump" | sed -n "1,5p"
else docker run --rm -i "${NODERA_DR_PG_IMAGE:-postgres:16-alpine}" pg_restore --list < "$WORK/nodera.dump" | sed -n "1,5p"; fi
echo "..."
[ "$MODE" = "verify" ] && { echo "verify OK (nothing was changed)"; exit 0; }
[ "$MODE" = "apply" ] || { echo "unknown mode $MODE" >&2; exit 1; }

echo "!! APPLY: replacing database 'nodera' in $PG. Ctrl-C within 10s to abort."; sleep 10
OW="$(tr -d '\n' < "$SECRETS/db-owner-password")"; AP="$(tr -d '\n' < "$SECRETS/db-app-password")"
docker exec "$PG" psql -U postgres -v ON_ERROR_STOP=1 -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='nodera' AND pid<>pg_backend_pid();" >/dev/null
docker exec "$PG" psql -U postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS nodera;" -c "CREATE DATABASE nodera;"
docker exec -i "$PG" psql -U postgres -d nodera -v ON_ERROR_STOP=1 -v owner_pw="$OW" -v app_pw="$AP" < "$HERE/db/01-roles.sql" >/dev/null
docker exec -i "$PG" pg_restore -U postgres -d nodera --no-owner --role=nodera_owner --exit-on-error < "$WORK/nodera.dump"
docker exec -i -e PGPASSWORD="$OW" "$PG" psql -h 127.0.0.1 -U nodera_owner -d nodera -v ON_ERROR_STOP=1 < "$HERE/db/02-post-migrate.sql" >/dev/null
echo "restored. Next: docker compose up -d (migrate runs first), then check /ready and the audit log."
