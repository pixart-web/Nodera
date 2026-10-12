#!/usr/bin/env bash
# Creates the secret files docker-compose.prod.yml mounts, with strong random
# values where Nodera owns the secret. Idempotent: an existing file is NEVER
# overwritten (rotating a key is a deliberate act, see docs/KEY-CUSTODY.md).
# Prints file names only, never values.
#
#   sudo scripts/init-secrets.sh                # /etc/nodera/secrets
#   sudo scripts/init-secrets.sh /path/to/dir
#
# Files that depend on an EXISTING external system (mariadb-root-password,
# backup-sftp-key) are created empty: fill them in yourself.
set -euo pipefail

DIR="${1:-/etc/nodera/secrets}"
GID="${NODERA_SECRETS_GID:-10100}"
command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 1; }

umask 077
mkdir -p "$DIR"

pw()  { openssl rand -base64 48 | tr -d '/+=\n' | cut -c1-40; }     # URL-safe, 40 chars
key() { openssl rand -base64 32 | tr -d '\n'; }                     # 32 random bytes, base64

put() { # put <name> <value-command...>
  local f="$DIR/$1"; shift
  if [ -e "$f" ]; then echo "kept     $f"; return; fi
  "$@" > "$f"
  echo "created  $f"
}
put_empty() { local f="$DIR/$1"; if [ -e "$f" ]; then echo "kept     $f"; else : > "$f"; echo "created  $f (EMPTY — fill in if used)"; fi; }
read_secret() { tr -d '\n' < "$DIR/$1"; }

put postgres-superuser-password pw
put db-owner-password pw
put db-app-password pw
put redis-password pw
put secrets-encryption-key key
put agent-signing-key key
put_empty mariadb-root-password
put_empty backup-sftp-key

# Connection strings derive from the passwords above (sslmode=disable: Postgres is
# only reachable on the internal Docker network, never across a network).
[ -e "$DIR/database-url" ]         || { printf 'postgres://nodera_app:%s@postgres:5432/nodera?sslmode=disable' "$(read_secret db-app-password)" > "$DIR/database-url"; echo "created  $DIR/database-url"; }
[ -e "$DIR/migrate-database-url" ] || { printf 'postgres://nodera_owner:%s@postgres:5432/nodera?sslmode=disable' "$(read_secret db-owner-password)" > "$DIR/migrate-database-url"; echo "created  $DIR/migrate-database-url"; }
[ -e "$DIR/redis-url" ]            || { printf 'redis://:%s@redis:6379' "$(read_secret redis-password)" > "$DIR/redis-url"; echo "created  $DIR/redis-url"; }

if [ "$(id -u)" -eq 0 ]; then
  chown -R "root:$GID" "$DIR"
  chmod 0750 "$DIR"
  chmod 0440 "$DIR"/*
else
  echo "NOTE: not root — left ownership alone. Run again with sudo so group $GID can read the files." >&2
fi

cat <<MSG

Done. NOW, before anything else (docs/KEY-CUSTODY.md):
  1. Copy secrets-encryption-key and agent-signing-key to OFFLINE custody
     (password manager + a second sealed copy). Losing the encryption key makes
     every stored Nodera secret and backup credential permanently unreadable.
  2. Do NOT store them in the same off-site location as the database backups.
MSG
