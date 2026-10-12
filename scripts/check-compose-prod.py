#!/usr/bin/env python3
"""Static invariants for docker-compose.prod.yml (run in CI and before deploys).

Reads the *rendered* configuration (`docker compose config --format json`), so it
checks what Docker would actually run, after variable interpolation. It cannot
prove runtime behaviour — that is what docs/HETZNER-STAGING-RUNBOOK.md is for —
but it makes the security-relevant decisions impossible to regress silently.

  NODERA_HOST=x ... python3 scripts/check-compose-prod.py [compose-file]
"""
import json, os, subprocess, sys

FILE = sys.argv[1] if len(sys.argv) > 1 else "docker-compose.prod.yml"
compose = ["docker", "compose"] if subprocess.run(["docker", "compose", "version"], capture_output=True).returncode == 0 else ["docker-compose"]
raw = subprocess.run(compose + ["-f", FILE, "config", "--format", "json"], capture_output=True, text=True)
if raw.returncode != 0:
    print(raw.stderr)
    sys.exit(1)
cfg = json.loads(raw.stdout)
svc = cfg["services"]
bad = []


def need(cond, msg):
    if not cond:
        bad.append(msg)


for name, s in svc.items():
    need("ports" not in s or not s["ports"], f"{name}: must not publish host ports (Traefik is the only entry point)")
    need("no-new-privileges:true" in s.get("security_opt", []), f"{name}: needs no-new-privileges")
    need(s.get("cap_drop") == ["ALL"], f"{name}: must drop ALL capabilities")
    need(s.get("read_only") is True, f"{name}: needs a read-only root filesystem")
    need(s.get("logging", {}).get("options", {}).get("max-size"), f"{name}: container logs must be size-bounded")
    need(s.get("privileged") is not True, f"{name}: must not be privileged")
    need(s.get("network_mode") not in ("host",), f"{name}: must not use host networking")
    need(not s.get("pid") and not s.get("ipc"), f"{name}: must not share host pid/ipc")
    img = s.get("image", "")
    tagged = "@sha256:" in img or ":" in img.rsplit("/", 1)[-1]
    need(tagged and not img.endswith(":latest"), f"{name}: image must be pinned to a tag or digest, never latest ({img})")
    need(s.get("deploy", {}).get("resources", {}).get("limits", {}).get("memory"), f"{name}: needs a memory limit")

sock = "/var/run/docker.sock"
holders = [n for n, s in svc.items() if any(v.get("source") == sock for v in s.get("volumes", []))]
need(holders == ["agent"], f"only the agent may mount the Docker socket, found: {holders}")

def api_env_trusted(svc):
    return svc["api"]["environment"].get("NODERA_TRUSTED_PROXIES")


for n in ("postgres", "redis", "migrate", "agent"):
    need(list(svc[n]["networks"].keys()) == ["internal"], f"{n}: must be on the internal-only network")
need(cfg["networks"]["internal"].get("internal") is True, "the 'internal' network must be internal-only")
need(cfg["networks"]["edge"].get("external") is True, "the edge network must be the external, operator-created Traefik network")
need(sorted(n for n, x in svc.items() if "edge" in x["networks"]) == ["api", "web"], "only api and web may join the edge network")
need("proxy" not in cfg["networks"], "control-plane services must not join the shared workload network")
need(bool(api_env_trusted(svc)), "api needs NODERA_TRUSTED_PROXIES")

api = svc["api"]["environment"]
need(api.get("NODERA_ENV") == "production", "api must run with NODERA_ENV=production")
need(api.get("NODERA_PROVIDER_MODE") == "agent", "api must use provider mode 'agent' (no Docker socket)")
need(api.get("NODERA_AUTO_MIGRATE") == "false", "api must not auto-migrate in production")
need(api.get("NODERA_CORS_ORIGINS", "").startswith("https://"), "CORS origin must be https")
for k, v in api.items():
    need(not (k.endswith(("KEY", "PASSWORD", "TOKEN", "URL")) and v and not k.endswith("_FILE") and k in (
        "NODERA_SECRETS_ENCRYPTION_KEY", "NODERA_AGENT_SIGNING_KEY", "NODERA_DATABASE_URL", "NODERA_REDIS_URL")),
         f"api: {k} must be supplied as a *_FILE secret, not an environment value")
need("NODERA_DATABASE_URL_FILE" in api and "NODERA_SECRETS_ENCRYPTION_KEY_FILE" in api, "api secrets must be file-based")

dep = svc["api"].get("depends_on", {})
need(dep.get("migrate", {}).get("condition") == "service_completed_successfully", "api must wait for the migration job to succeed")
need(svc["migrate"].get("restart") == "no", "migrate must be a one-shot job")

labels = svc["api"]["labels"]
need(any("PathPrefix(`/api/`)" in v for v in labels.values()), "api router must route /api/ by path")
need(any(k.endswith("responseforwarding.flushinterval") for k in labels), "SSE: api service needs an explicit flush interval")
for n in ("postgres", "redis", "api", "web", "agent", "migrate"):
    need(svc[n]["labels"].get("nodera.role") == "control-plane", f"{n}: must carry nodera.role=control-plane (agent protection)")
    need(svc[n].get("container_name", "").startswith("nodera-cp-"), f"{n}: container_name must live in the protected nodera-cp- namespace")

if bad:
    print("docker-compose.prod.yml invariant violations:")
    for b in bad:
        print("  -", b)
    sys.exit(1)
print(f"{FILE}: {len(svc)} services, all invariants hold")
