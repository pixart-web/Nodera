#!/usr/bin/env bash
# Run after every `nodera-migrate` (the compose `migrate` service). Idempotent.
# Re-applies the database-level protections (append-only audit_log, read-only
# schema_migrations for the app role) and PROVES them as the runtime role.
# Fails loudly — treat a failure as "do not take traffic".
#
#   sudo scripts/post-migrate.sh              # secrets in /etc/nodera/secrets, container nodera-cp-postgres
set -euo pipefail
SECRETS="${NODERA_SECRETS_DIR:-/etc/nodera/secrets}"
PG="${NODERA_DR_PG_CONTAINER:-nodera-cp-postgres}"
HERE="$(cd "$(dirname "$0")" && pwd)"
OW="$(tr -d '\n' < "$SECRETS/db-owner-password")"; AP="$(tr -d '\n' < "$SECRETS/db-app-password")"

docker exec -i -e PGPASSWORD="$OW" "$PG" psql -h 127.0.0.1 -U nodera_owner -d nodera -v ON_ERROR_STOP=1 -q < "$HERE/db/02-post-migrate.sql"
out="$(docker exec -i -e PGPASSWORD="$AP" "$PG" psql -h 127.0.0.1 -U nodera_app -d nodera -v ON_ERROR_STOP=1 < "$HERE/db/03-verify-roles.sql" 2>&1)"
pass="$(printf '%s\n' "$out" | grep -c 'PASS' || true)"
if printf '%s\n' "$out" | grep -q 'FAIL' || [ "$pass" -lt 10 ]; then
  printf '%s\n' "$out" >&2
  echo "post-migrate: role verification FAILED ($pass passes)" >&2
  exit 1
fi
echo "post-migrate: database protections applied and verified ($pass checks)"
