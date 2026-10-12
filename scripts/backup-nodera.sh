#!/usr/bin/env bash
# Back up NODERA ITSELF (the control plane), not customer workloads.
#
# Produces ONE age-encrypted package containing:
#   nodera.dump        pg_dump -Fc of the `nodera` database (custom format)
#   manifest.json      timestamp, schema version, image refs, sha256 of the dump
#   config/            the non-secret deployment config: compose file + env file
# and uploads it to the Storage Box over SFTP (pinned host key), verifies the
# upload by downloading it back, then applies retention.
#
# It deliberately does NOT contain NODERA_SECRETS_ENCRYPTION_KEY, the agent
# signing key, database passwords or the SFTP key. Those live in operator-held
# custody (docs/KEY-CUSTODY.md): a backup that carried the key next to the data it
# protects would protect nothing. The package is encrypted to an age PUBLIC key,
# so this host can write backups but cannot read them back.
#
# Config: /etc/nodera/backup.env (see docs/BACKUPS.md) — values, not secrets:
#   NODERA_DR_AGE_RECIPIENT   age public key (age1...)           REQUIRED
#   NODERA_DR_SFTP_TARGET     user@host of the Storage Box       (empty = local only, loud warning)
#   NODERA_DR_SFTP_PORT       default 23
#   NODERA_DR_SFTP_KEY        path to the private key file
#   NODERA_DR_KNOWN_HOSTS     path to a known_hosts file holding ONLY the pinned host key
#   NODERA_DR_REMOTE_DIR      default nodera-dr
#   NODERA_DR_KEEP            number of packages to keep remotely (default 14, minimum 3)
#   NODERA_DR_LOCAL_DIR       default /var/backups/nodera (keeps the 2 newest)
#   NODERA_DR_PG_CONTAINER    default nodera-cp-postgres
#   NODERA_DR_COMPOSE_DIR     directory with docker-compose.prod.yml (default /opt/nodera)
#   NODERA_DR_ENV_FILE        default /etc/nodera/nodera.env
set -euo pipefail
umask 077

CONF="${NODERA_DR_CONF:-/etc/nodera/backup.env}"
[ -r "$CONF" ] && . "$CONF"

: "${NODERA_DR_AGE_RECIPIENT:?set NODERA_DR_AGE_RECIPIENT (age public key) in $CONF}"
PG="${NODERA_DR_PG_CONTAINER:-nodera-cp-postgres}"
LOCAL="${NODERA_DR_LOCAL_DIR:-/var/backups/nodera}"
COMPOSE_DIR="${NODERA_DR_COMPOSE_DIR:-/opt/nodera}"
ENV_FILE="${NODERA_DR_ENV_FILE:-/etc/nodera/nodera.env}"
KEEP="${NODERA_DR_KEEP:-14}"; [ "$KEEP" -ge 3 ] || KEEP=3
REMOTE_DIR="${NODERA_DR_REMOTE_DIR:-nodera-dr}"
PORT="${NODERA_DR_SFTP_PORT:-23}"
for c in docker age sha256sum tar; do command -v "$c" >/dev/null || { echo "missing: $c" >&2; exit 1; }; done
SHA="sha256sum"; command -v sha256sum >/dev/null || SHA="shasum -a 256"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
NAME="nodera-dr-$TS.tar.age"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
mkdir -p "$LOCAL" "$WORK/pkg/config"

echo "[$TS] dumping database from $PG"
docker exec "$PG" pg_dump -U postgres -d nodera -Fc --no-owner > "$WORK/pkg/nodera.dump"
[ -s "$WORK/pkg/nodera.dump" ] || { echo "empty dump — aborting" >&2; exit 1; }
# A dump that cannot be listed is not a backup.
docker exec -i "$PG" pg_restore --list < "$WORK/pkg/nodera.dump" >/dev/null

SCHEMA="$(docker exec "$PG" psql -U postgres -d nodera -tAc 'select coalesce(max(version),0) from schema_migrations')"
DUMP_SHA="$($SHA "$WORK/pkg/nodera.dump" | cut -d' ' -f1)"
[ -r "$COMPOSE_DIR/docker-compose.prod.yml" ] && cp "$COMPOSE_DIR/docker-compose.prod.yml" "$WORK/pkg/config/"
[ -r "$ENV_FILE" ] && cp "$ENV_FILE" "$WORK/pkg/config/nodera.env"
IMAGES="$(docker ps --filter 'label=nodera.role=control-plane' --format '{{.Names}}={{.Image}}' | sort | paste -sd, -)"
printf '{"created_utc":"%s","schema_version":%s,"dump_sha256":"%s","images":"%s","host":"%s"}\n' \
  "$TS" "${SCHEMA:-0}" "$DUMP_SHA" "$IMAGES" "$(hostname)" > "$WORK/pkg/manifest.json"

tar -C "$WORK/pkg" -cf - . | age -r "$NODERA_DR_AGE_RECIPIENT" -o "$WORK/$NAME"
PKG_SHA="$($SHA "$WORK/$NAME" | cut -d' ' -f1)"
cp "$WORK/$NAME" "$LOCAL/$NAME"
echo "$PKG_SHA  $NAME" > "$LOCAL/$NAME.sha256"
echo "local copy: $LOCAL/$NAME ($(wc -c < "$LOCAL/$NAME") bytes)"

# Keep only the 2 newest local packages.
ls -1t "$LOCAL"/nodera-dr-*.tar.age 2>/dev/null | tail -n +3 | while read -r old; do rm -f "$old" "$old.sha256"; done

if [ -z "${NODERA_DR_SFTP_TARGET:-}" ]; then
  echo "WARNING: NODERA_DR_SFTP_TARGET is empty — this package exists ONLY on this host and is NOT a disaster-recovery copy." >&2
  exit 0
fi
: "${NODERA_DR_SFTP_KEY:?set NODERA_DR_SFTP_KEY}" "${NODERA_DR_KNOWN_HOSTS:?set NODERA_DR_KNOWN_HOSTS (pinned host key)}"
SFTP=(sftp -q -P "$PORT" -i "$NODERA_DR_SFTP_KEY" -o BatchMode=yes -o IdentitiesOnly=yes
      -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$NODERA_DR_KNOWN_HOSTS" -o GlobalKnownHostsFile=/dev/null)

echo "uploading to $NODERA_DR_SFTP_TARGET:$REMOTE_DIR/$NAME"
printf -- '-mkdir %s\nput %s %s/%s.part\nrename %s/%s.part %s/%s\n' "$REMOTE_DIR" "$WORK/$NAME" "$REMOTE_DIR" "$NAME" "$REMOTE_DIR" "$NAME" "$REMOTE_DIR" "$NAME" \
  | "${SFTP[@]}" -b - "$NODERA_DR_SFTP_TARGET"

echo "verifying by downloading it back"
printf 'get %s/%s %s/verify.tar.age\n' "$REMOTE_DIR" "$NAME" "$WORK" | "${SFTP[@]}" -b - "$NODERA_DR_SFTP_TARGET"
BACK_SHA="$($SHA "$WORK/verify.tar.age" | cut -d' ' -f1)"
[ "$BACK_SHA" = "$PKG_SHA" ] || { echo "UPLOAD VERIFICATION FAILED: checksum mismatch" >&2; exit 2; }
echo "off-site copy verified (sha256 $PKG_SHA)"

# Retention: list, keep the newest $KEEP, delete the rest.
printf 'ls -1 %s\n' "$REMOTE_DIR" | "${SFTP[@]}" -b - "$NODERA_DR_SFTP_TARGET" 2>/dev/null \
  | sed 's#.*/##' | grep -E '^nodera-dr-[0-9TZ]+\.tar\.age$' | sort -r | tail -n +$((KEEP + 1)) > "$WORK/prune.txt" || true
if [ -s "$WORK/prune.txt" ]; then
  while read -r old; do printf 'rm %s/%s\n' "$REMOTE_DIR" "$old"; done < "$WORK/prune.txt" \
    | "${SFTP[@]}" -b - "$NODERA_DR_SFTP_TARGET"
  echo "pruned $(wc -l < "$WORK/prune.txt") old package(s)"
fi
echo "done"
