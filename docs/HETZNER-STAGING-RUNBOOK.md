# Hetzner staging runbook

Goal: install Nodera on **one** Hetzner server, validate it with **non-customer** test workloads, break it on purpose, and only then
consider customer workloads (`docs/PRODUCTION-GATE.md`). Every command here either was exercised on a local Linux VM against the real images
and compose file, or is standard host administration and is marked **(host, untested by us)**. Nothing in this document has been run on the Hetzner host yet — the
first run is the validation, and anything that surprises you is a finding to write down, not to paper over.

Conventions: `NODERA_HOST` = the public hostname (e.g. `nodera-staging.example.com`). Commands run as root or with `sudo`. Replace `X.Y.Z` with the release tag.

## 0. Before you start

* A Hetzner server (CX/CPX 2 vCPU / 4 GB for staging; `docs/DEPLOYMENT.md` sizing), a **DNS A/AAAA record** for `NODERA_HOST` pointing at it, and a Storage Box (SFTP, port 23) with an SSH key you control.
* A green CI run and a published release `vX.Y.Z` (`docs/RELEASES.md`). The `release.yml` workflow has not run yet: pushing the first tag is part of this exercise.
* Your **operator workstation** has `age` (`age-keygen`) for the control-plane backups.
* Read `docs/KEY-CUSTODY.md` first. You will be creating keys whose only safe copy is off this server.

## 1. Host preparation (host, untested by us)

Target OS: **Debian 12 or Ubuntu 24.04 LTS**, fresh install, root SSH key only.

```bash
# 1.1 updates + unattended security updates + time sync
apt-get update && apt-get -y full-upgrade
apt-get -y install unattended-upgrades chrony fail2ban ufw acl curl ca-certificates git age
dpkg-reconfigure -plow unattended-upgrades
systemctl enable --now chrony fail2ban
timedatectl            # "System clock synchronized: yes" — signed agent requests allow ±60 s of skew

# 1.2 SSH policy: keys only, no root password, restricted users
install -d -m 0755 /etc/ssh/sshd_config.d
cat > /etc/ssh/sshd_config.d/10-nodera.conf <<'EOF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
EOF
sshd -t && systemctl reload ssh      # keep your current session open; test a second login before closing it

# 1.3 firewall: only SSH (from your admin address) and web. Docker publishes ports by editing iptables DIRECTLY,
#     bypassing ufw: only Traefik may publish ports (80/443). Nothing else in this stack publishes any.
ufw default deny incoming && ufw default allow outgoing
ufw allow from ADMIN_IP to any port 22 proto tcp      # or allow 22/tcp if you have no fixed address, and rely on fail2ban
ufw allow 80/tcp && ufw allow 443/tcp
ufw enable
```

```bash
# 1.4 Docker Engine + Compose plugin from Docker's official repository (follow https://docs.docker.com/engine/install/ for your distro), then:
docker version && docker compose version
cat > /etc/docker/daemon.json <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "5" },
  "live-restore": true
}
EOF
systemctl restart docker
# Workload containers created by the agent inherit these log limits (compose services set their own).

# 1.5 directories, groups and networks
groupadd -g 10100 nodera-secrets 2>/dev/null || true
install -d -m 0750 -o root -g 10100 /etc/nodera /etc/nodera/secrets
install -d -m 2750 -o 10001 -g 10001 /srv/nodera/data          # shared by api + agent; uid 10001 owns it
install -d -m 0700 /var/backups/nodera /etc/nodera/dr
docker network create nodera-edge        # Traefik <-> api/web ONLY
docker network create proxy-public       # shared network that workloads (WordPress, MariaDB) join
getent group docker | cut -d: -f3        # note this GID -> NODERA_DOCKER_GID
```

Verify: `ufw status verbose`; `ss -tlnp` shows only sshd (and, after §2, Traefik on 80/443).

## 2. Traefik (skip if you already run one — check §2.2)

### 2.1 Minimal Traefik matching the contract

```bash
install -d /opt/traefik && cd /opt/traefik
cat > traefik.yml <<'EOF'
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint: { to: websecure, scheme: https }
  websecure:
    address: ":443"
providers:
  docker:
    exposedByDefault: false
certificatesResolvers:
  letsencrypt:
    acme:
      email: you@example.com
      storage: /acme/acme.json
      httpChallenge: { entryPoint: web }
log: { level: INFO }
EOF
cat > docker-compose.yml <<'EOF'
services:
  traefik:
    image: traefik:v3.6          # must support your Docker Engine's API version (v3.1 does not support Docker 29)
    container_name: traefik       # protected by name: Nodera operations cannot touch it
    restart: unless-stopped
    ports: ["80:80", "443:443"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./traefik.yml:/etc/traefik/traefik.yml:ro
      - acme:/acme
    networks: [nodera-edge, proxy-public]
    security_opt: ["no-new-privileges:true"]
    logging: { driver: json-file, options: { max-size: "10m", max-file: "5" } }
networks:
  nodera-edge: { external: true }
  proxy-public: { external: true }
volumes:
  acme:
EOF
docker compose up -d && docker logs traefik 2>&1 | tail
```

### 2.2 If Traefik already exists

It must: use the Docker provider with `exposedByDefault: false`; be attached to **`nodera-edge`** (add it to *its* compose `networks:` so it survives recreation) and to `proxy-public`;
define entrypoint `websecure` and a certificate resolver (`letsencrypt`, or set `NODERA_CERT_RESOLVER`); redirect HTTP→HTTPS; run a v3 release that can talk to your Docker Engine
(`docker logs traefik` must show no "client version … is too old" errors). Record the container name; if it is not `traefik`, add it to `NODERA_PROTECTED_CONTAINERS`.

## 3. MariaDB (the managed workloads' database)

Nodera manages an **existing** MariaDB container through `docker exec`; it does not create the server. If you do not have one for staging:

```bash
MPW="$(openssl rand -base64 36 | tr -d '/+=\n' | cut -c1-32)"
docker run -d --name mariadb --restart unless-stopped --network proxy-public \
  -e MARIADB_ROOT_PASSWORD="$MPW" -v mariadb_data:/var/lib/mysql mariadb:11
printf '%s' "$MPW" > /etc/nodera/secrets/mariadb-root-password   # (created empty by init-secrets.sh in §5; write it after)
docker exec mariadb mariadb --version         # < 10.5 ? set NODERA_MARIADB_LEGACY_CLIENT=true
```

The container is named `mariadb` (protected by name). The root password is read by the agent only, from a file.

## 4. Get the release

```bash
git clone --branch vX.Y.Z --depth 1 https://github.com/pixart-web/Nodera.git /opt/nodera    # scripts, compose file, templates — no source is built on the host
cd /opt/nodera && git describe --tags
```

## 5. Configuration and keys

```bash
cp deploy/nodera.env.example /etc/nodera/nodera.env && chmod 0640 /etc/nodera/nodera.env
${EDITOR:-nano} /etc/nodera/nodera.env
#   NODERA_HOST, NODERA_API_IMAGE/WEB/AGENT (tags from the release run — digests preferred), NODERA_DOCKER_GID,
#   NODERA_TRUSTED_PROXIES = output of:  docker network inspect nodera-edge --format '{{(index .IPAM.Config 0).Subnet}}'
#   NODERA_MARIADB_CONTAINER=mariadb, NODERA_WORDPRESS_DB_HOST=mariadb
scripts/init-secrets.sh            # creates /etc/nodera/secrets/* (never overwrites; prints names only)
```

**Stop here and do the custody step** (`docs/KEY-CUSTODY.md`): copy `secrets-encryption-key` and `agent-signing-key` to offline custody *now*. If you wrote the MariaDB password, make sure
`/etc/nodera/secrets/mariadb-root-password` is mode `0440`, group `10100`. Check: `ls -ln /etc/nodera/secrets` (all `root:10100`, `0440`).

## 6. Database: roles, migration, protections

```bash
cd /opt/nodera
docker compose --env-file /etc/nodera/nodera.env -f docker-compose.prod.yml pull
docker compose --env-file /etc/nodera/nodera.env -f docker-compose.prod.yml up -d postgres
OW="$(cat /etc/nodera/secrets/db-owner-password)"; AP="$(cat /etc/nodera/secrets/db-app-password)"
docker exec -i nodera-cp-postgres psql -U postgres -v ON_ERROR_STOP=1 -v owner_pw="$OW" -v app_pw="$AP" < scripts/db/01-roles.sql
docker compose --env-file /etc/nodera/nodera.env -f docker-compose.prod.yml up migrate      # expect: "migrate: ok (32 pending applied, 32 total)"
scripts/post-migrate.sh                                                                    # expect: "database protections applied and verified (10 checks)"
```

## 7. Start Nodera

```bash
docker compose --env-file /etc/nodera/nodera.env -f docker-compose.prod.yml up -d
docker compose --env-file /etc/nodera/nodera.env -f docker-compose.prod.yml ps        # postgres, redis, api, web, agent healthy; migrate exited 0
docker exec nodera-cp-api wget -qO- http://127.0.0.1:8080/ready                          # agent: "not_configured" until §8 — expected
curl -sI https://NODERA_HOST/login | head -3                                              # 200 over HTTPS, valid Let's Encrypt certificate
```

The agent container waits ("not enrolled yet") until §8.

## 8. First administrator, organisation, agent

Everything below uses the UI at `https://NODERA_HOST`, or `scripts/staging-smoke.py` which drives the same API (`bootstrap` does steps 1–4; use a throw-away operator for the smoke tests and your real account for administration).

1. **Sign up** your real account in the UI (no email verification exists yet — `NOT IMPLEMENTED`; the password policy rejects weak passwords).
2. Grant yourself platform permissions: set `NODERA_PLATFORM_BOOTSTRAP_ADMIN_EMAIL=you@example.com` in `nodera.env`, then `docker compose … up -d api`. Check the API log (`docker logs nodera-cp-api | grep -i bootstrap`) for `bootstrapped platform administrator` — it only logs a warning (`does not match any existing user`) if the email has not signed up yet.
3. **Create the first organisation** in the UI (or `POST /api/v1/organizations`).
4. **Register the node** (Infrastructure → Nodes, `provider: hetzner`) and create an **agent enrolment token** for it (30 min, single use, shown once).
5. **Enrol the agent** inside its container (the Docker socket is already mounted):
   ```bash
   docker exec nodera-cp-agent nodera-agent enroll --api http://api:8080 --token ndr_enr_…
   ```
   (plain `http://api:8080` is accepted only because `api` is a single-label name on the internal network; a remote agent must use `https://`.)
   Note the printed agent id.
6. Put it in `nodera.env`: `NODERA_LOCAL_AGENT_ID=<agent id>`, then `docker compose … up -d api`. Within ~10 s the agent log shows heartbeats and
   `/ready` reports `"agent":"ok"`, `provider.containers: "agent"`, `provider.database: "agent"`.
7. Platform **secrets** (e.g. provider tokens) are added in the UI after sign-in; nothing is configured in files beyond §5.

## 9. Back up Nodera itself (do this before anything else is created)

```bash
# on your workstation (NOT the server):
age-keygen -o nodera-dr-identity.txt      # store this file in custody (password manager + second sealed copy); the "Public key: age1…" line goes into backup.env
# on the server:
cp /opt/nodera/deploy/backup.env.example /etc/nodera/backup.env && chmod 0640 /etc/nodera/backup.env
ssh-keygen -t ed25519 -N '' -f /etc/nodera/dr/sftp_key                          # add /etc/nodera/dr/sftp_key.pub to the Storage Box (authorized_keys)
ssh-keyscan -p 23 uXXXXXX.your-storagebox.de | grep ed25519 > /etc/nodera/dr/known_hosts
ssh-keygen -lf /etc/nodera/dr/known_hosts     # compare this fingerprint with Hetzner's published one before trusting it
${EDITOR:-nano} /etc/nodera/backup.env        # recipient, target, port 23, key + known_hosts paths, remote dir (create it on the Box first)
/opt/nodera/scripts/backup-nodera.sh          # expect: "off-site copy verified (sha256 …)"
# on your workstation, with the package fetched back from the Box:
scripts/restore-nodera.sh verify nodera-dr-….tar.age nodera-dr-identity.txt     # read-back test; lists the dump with pg_restore (or a throw-away postgres container)
```

Schedule it (daily, and before every release):

```bash
cat > /etc/systemd/system/nodera-backup.service <<'EOF'
[Unit]
Description=Back up Nodera control plane
[Service]
Type=oneshot
ExecStart=/opt/nodera/scripts/backup-nodera.sh
EOF
cat > /etc/systemd/system/nodera-backup.timer <<'EOF'
[Unit]
Description=Daily Nodera control-plane backup
[Timer]
OnCalendar=*-*-* 03:15:00
Persistent=true
RandomizedDelaySec=300
[Install]
WantedBy=timers.target
EOF
systemctl daemon-reload && systemctl enable --now nodera-backup.timer && systemctl list-timers nodera-backup.timer
```

A failed run exits non-zero: watch `journalctl -u nodera-backup` (alerting is yours to wire; there is none built in).

## 10. Off-site backups for customer workloads

In `nodera.env` set the `NODERA_BACKUP_SFTP_*` values (`docs/BACKUPS.md` §3), put the Storage Box **private key** in `/etc/nodera/secrets/backup-sftp-key` (`0440 root:10100`, no passphrase),
pin the host key as `ssh-ed25519 SHA256:…` (from the `ssh-keygen -lf` above), `NODERA_BACKUP_DESTINATION=sftp`, then `docker compose … up -d api`.
`/ready` must show `provider.backups: "offsite"`; the Dashboard "Providers" panel shows "off-site". Create the remote directory on the Box first.

## 11. Validate

1. `docs/SMOKE-TESTS.md` — every item, PASS/FAIL recorded.
2. `docs/DISASTER-TESTS.md` — all 20, PASS/FAIL recorded.
3. `docs/PRODUCTION-GATE.md` — every line satisfied with evidence.

Workload permissions note (found locally, **must be re-checked on the real filesystem**): managed containers run as `NODERA_WORKLOAD_USER` (default: the API's uid:gid, 10001). The data directory is `2750 10001:10001`.
Provision a test WordPress project and confirm `ls -ln /srv/nodera/data/fs/projects/<slug>/data` shows uid 10001 and that a backup **and a restore** of it succeed.

## 12. Removing the staging install

`docker compose … down` keeps volumes. Removing data (`docker volume rm nodera_postgres_data nodera_agent_state`, `rm -rf /srv/nodera/data`) is irreversible: do it only on staging, after confirming off-site copies are not needed.
