#!/usr/bin/env python3
"""Nodera staging smoke driver (stdlib only).

Drives the PUBLIC API the same way a browser or API client would, so it also
exercises the reverse proxy, TLS, cookies and CSRF. It is a verification aid for
docs/HETZNER-STAGING-RUNBOOK.md, not a test framework: every check prints
PASS/FAIL and the exit code is non-zero if any FAIL happened.

  staging-smoke.py --base https://nodera.example.com --state ./smoke.json bootstrap
  staging-smoke.py --base ...                       --state ./smoke.json security
  staging-smoke.py --base ...                       --state ./smoke.json provision

* `bootstrap`  signs up the smoke operator (password generated, kept in --state,
               mode 0600, never printed), creates an org + node and prints a
               one-time AGENT ENROLMENT TOKEN to feed to `nodera-agent enroll`.
* `security`   cookie flags, CSRF enforcement, bearer vs cookie, unauthenticated
               access, tenant isolation between two throw-away orgs.
* `approval-race` fires 8 simultaneous approvals of one restore request: exactly one
               may succeed (200), the rest must get 409, and the restore must run once.
* `provision`  creates a TEST project (never customer data), runs the provisioning
               operation and waits, printing step results. Needs the agent enrolled
               and NODERA_LOCAL_AGENT_ID set.

--insecure skips TLS verification: only for a throw-away local stack with a
self-signed certificate. Never use it against the real host.
"""
import argparse, http.cookiejar, json, os, secrets, ssl, sys, time, urllib.error, urllib.request

FAILED = []


def check(ok, name, detail=""):
    print(("PASS  " if ok else "FAIL  ") + name + (f"  [{detail}]" if detail and not ok else ""))
    if not ok:
        FAILED.append(name)
    return ok


class Client:
    def __init__(self, base, insecure, host_header=""):
        self.base = base.rstrip("/")
        self.host_header = host_header
        ctx = ssl.create_default_context()
        if insecure:
            ctx.check_hostname, ctx.verify_mode = False, ssl.CERT_NONE
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(self.jar), urllib.request.HTTPSHandler(context=ctx))
        self.org = None

    def cookie(self, name):
        for c in self.jar:
            if c.name == name:
                return c
        return None

    def call(self, method, path, body=None, headers=None, csrf=True, org=True, raw=False):
        h = {"Content-Type": "application/json"}
        if self.host_header:
            h["Host"] = self.host_header  # local test only: connect to an IP, route by Host
        h.update(headers or {})
        if method != "GET" and csrf and self.cookie("nodera_csrf"):
            h.setdefault("X-CSRF-Token", self.cookie("nodera_csrf").value)
        if org and self.org:
            h.setdefault("X-Nodera-Org", self.org)
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base + path, data=data, method=method, headers=h)
        try:
            with self.opener.open(req, timeout=30) as r:
                payload = r.read()
                return r.status, (payload if raw else (json.loads(payload) if payload else None)), r.headers
        except urllib.error.HTTPError as e:
            payload = e.read()
            try:
                return e.code, json.loads(payload), e.headers
            except Exception:
                return e.code, payload.decode(errors="replace"), e.headers


def load(path):
    return json.load(open(path)) if os.path.exists(path) else {}


def save(path, st):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(st, f)


def login(c, st):
    s, _, _ = c.call("POST", "/api/v1/auth/login", {"email": st["email"], "password": st["password"]}, csrf=False, org=False)
    if not check(s == 200, "login", str(s) + (" (rate limited: wait 5 minutes)" if s == 429 else "")):
        sys.exit(1)


def bootstrap(c, st, args):
    if "email" not in st:
        st["email"] = f"smoke-{secrets.token_hex(4)}@nodera.test"
        st["password"] = secrets.token_urlsafe(24) + "-Aa9"
        s, _, _ = c.call("POST", "/api/v1/auth/signup", {"email": st["email"], "password": st["password"], "display_name": "Staging Smoke"}, csrf=False, org=False)
        check(s == 201, "signup", str(s))
        save(args.state, st)
    login(c, st)
    if "org" not in st:
        slug = "smoke-" + secrets.token_hex(3)
        s, body, _ = c.call("POST", "/api/v1/organizations", {"name": "Staging smoke " + slug, "slug": slug}, org=False)
        if check(s == 201, "create organization", str(s)):
            st["org"] = body["id"]
            save(args.state, st)
    c.org = st["org"]
    if "node" not in st:
        s, body, _ = c.call("POST", "/api/v1/infrastructure/nodes", {"hostname": args.node_name, "provider": "hetzner", "role": "app"})
        if check(s == 201, "register node", str(s)):
            st["node"] = body["id"]
            save(args.state, st)
    s, body, _ = c.call("POST", f"/api/v1/infrastructure/nodes/{st['node']}/agent-registrations", {})
    if check(s in (200, 201), "create agent enrolment token", str(s)):
        tok = body.get("token", "")
        print("\nEnrol the agent (token is single-use and short-lived):")
        print(f"  docker compose exec agent nodera-agent enroll --api http://api:8080 --token {tok}\n")


def security(c, st, args):
    login(c, st)
    sess, csrf = c.cookie("nodera_session"), c.cookie("nodera_csrf")
    check(bool(sess and sess.has_nonstandard_attr("HttpOnly")), "session cookie is HttpOnly")
    check(bool(sess and sess.secure), "session cookie is Secure", "plain-http/--insecure local test?")
    check(bool(csrf and not csrf.has_nonstandard_attr("HttpOnly") and csrf.secure), "CSRF cookie is readable by JS and Secure")
    c.org = st["org"]
    s, _, _ = c.call("POST", "/api/v1/clients", {"name": "csrf-probe"}, headers={"X-CSRF-Token": "wrong"}, csrf=False)
    check(s == 403, "mutation with a wrong CSRF token is refused", str(s))
    s, _, _ = c.call("POST", "/api/v1/clients", {"name": "csrf-probe"}, csrf=False)
    check(s == 403, "mutation without a CSRF token is refused", str(s))
    probe = "csrf-probe-ok-" + secrets.token_hex(3)
    s, _, _ = c.call("POST", "/api/v1/clients", {"name": probe})
    check(s == 201, "mutation with the CSRF token succeeds", str(s))
    anon = Client(args.base, args.insecure, args.host_header)
    s, _, _ = anon.call("GET", "/api/v1/projects", org=False)
    check(s == 401, "unauthenticated request is refused", str(s))
    # Tenant isolation: a second user in a second org must not see the first org's data.
    other = Client(args.base, args.insecure, args.host_header)
    em, pw = f"other-{secrets.token_hex(4)}@nodera.test", secrets.token_urlsafe(24) + "-Aa9"
    other.call("POST", "/api/v1/auth/signup", {"email": em, "password": pw, "display_name": "Other"}, csrf=False, org=False)
    other.call("POST", "/api/v1/auth/login", {"email": em, "password": pw}, csrf=False, org=False)
    slug = "other-" + secrets.token_hex(3)
    s, body, _ = other.call("POST", "/api/v1/organizations", {"name": slug, "slug": slug}, org=False)
    other.org = body["id"] if s == 201 else None
    s, body, _ = other.call("GET", "/api/v1/clients")
    names = [x.get("name") for x in (body or [])] if isinstance(body, list) else []
    check(s == 200 and probe not in names, "second tenant cannot list the first tenant's clients", f"{s} {names}")
    other.org = st["org"]
    s, _, _ = other.call("GET", "/api/v1/clients")
    check(s in (403, 404), "second tenant cannot address the first tenant's org", str(s))
    # Machine clients: a bearer API token works without cookies/CSRF but is limited to its scopes.
    s, body, _ = c.call("POST", "/api/v1/api-tokens", {"name": "staging-smoke", "scopes": ["clients.read"]})
    if check(s == 201 and "token" in (body or {}), "create a scoped API token", str(s)):
        bearer = Client(args.base, args.insecure, args.host_header)
        bearer.org = st["org"]
        auth = {"Authorization": "Bearer " + body["token"]}
        s, _, _ = bearer.call("GET", "/api/v1/clients", headers=auth)
        check(s == 200, "bearer token reads within its scope (no cookie, no CSRF)", str(s))
        s, _, _ = bearer.call("GET", "/api/v1/audit", headers=auth)
        check(s == 403, "bearer token cannot read outside its scope", str(s))
        s, _, _ = bearer.call("POST", "/api/v1/clients", {"name": "x"}, headers=auth)
        check(s == 403, "bearer token cannot write without the scope", str(s))
        s, _, _ = bearer.call("GET", "/api/v1/clients", headers={"Authorization": "Bearer ndr_not_a_real_token"})
        check(s == 401, "a made-up token is refused", str(s))
        if "id" in (body.get("info") or {}):
            c.call("DELETE", f"/api/v1/api-tokens/{body['info']['id']}")
    s, body, _ = c.call("GET", "/api/v1/audit?limit=50")
    acts = [e.get("action") for e in (body.get("items", body) if isinstance(body, dict) else body or [])]
    check(s == 200 and any(a and a.startswith("identity.") for a in acts), "audit events are recorded and readable", f"{s} {acts[:5]}")
    s, _, _ = c.call("GET", "/api/v1/platform/audit", org=False)
    check(s == 403, "a normal organisation owner is NOT a platform administrator", str(s))
    s, _, _ = c.call("POST", "/api/v1/auth/logout", {}, org=False)
    check(s in (200, 204), "logout", str(s))
    s, _, _ = c.call("GET", "/api/v1/projects")
    check(s == 401, "session is dead after logout", str(s))


def wait_op(c, op_id, timeout=300):
    end = time.time() + timeout
    while time.time() < end:
        s, op, _ = c.call("GET", f"/api/v1/operations/{op_id}")
        if s == 200 and op.get("status") in ("succeeded", "failed", "cancelled"):
            return op
        time.sleep(2)
    return None


def provision(c, st, args):
    login(c, st)
    c.org = st["org"]
    slug = args.project or "smoke-" + secrets.token_hex(3)
    s, p, _ = c.call("POST", "/api/v1/projects", {"name": "Smoke test " + slug, "slug": slug, "kind": "wordpress", "node_id": st.get("node")})
    if not check(s == 201, "create TEST project", f"{s} {p}"):
        return
    s, ref, _ = c.call("POST", f"/api/v1/projects/{p['id']}/provision", {}, headers={"Idempotency-Key": secrets.token_hex(8)})
    if not check(s == 202, "submit provisioning", f"{s} {ref}"):
        return
    op = wait_op(c, ref["id"] if "id" in ref else ref.get("job_id"))
    s, steps, _ = c.call("GET", f"/api/v1/operations/{op['id']}/steps") if op else (0, [], None)
    for stp in steps or []:
        print(f"      step {stp.get('name'):<28} {stp.get('status')}")
    check(bool(op) and op["status"] == "succeeded", "provisioning operation succeeded", json.dumps(op)[:400] if op else "timed out")
    st["project"], st["project_slug"] = p["id"], slug
    save(args.state, st)


def approval_race(c, st, args):
    """Fire many simultaneous approvals of ONE restore request: exactly one may win."""
    import concurrent.futures, collections
    login(c, st)
    c.org = st["org"]
    s, bl, _ = c.call("GET", "/api/v1/backups")
    items = bl["items"] if isinstance(bl, dict) and "items" in bl else bl
    done = sorted([b for b in items if b.get("status") == "completed"], key=lambda b: b["created_at"])
    if not check(bool(done), "a completed backup exists to restore (run `provision` and create a backup first)"):
        return
    s, ref, _ = c.call("POST", f"/api/v1/backups/{done[-1]['id']}/restore", {}, headers={"Idempotency-Key": secrets.token_hex(8)})
    if not check(s == 202 and ref.get("status") == "approval_required", "restore waits for approval", f"{s} {ref}"):
        return
    n = 8
    def decide(_):
        return c.call("POST", f"/api/v1/approvals/{ref['approval_id']}/decide", {"approve": True, "reason": "race test"})[0]
    with concurrent.futures.ThreadPoolExecutor(max_workers=n) as pool:
        codes = collections.Counter(pool.map(decide, range(n)))
    print(f"      decisions: {dict(codes)}")
    check(codes.get(200, 0) == 1 and codes.get(200, 0) + codes.get(409, 0) == n, "exactly one concurrent approval wins; the rest get 409", str(dict(codes)))
    time.sleep(5)
    s, ol, _ = c.call("GET", "/api/v1/operations?limit=20")
    ops = ol["items"] if isinstance(ol, dict) and "items" in ol else ol
    restores = [o for o in ops if o.get("operation") == "backup.restore" and o.get("created_at", "") >= done[-1]["created_at"]]
    newest = [o for o in restores if o.get("created_at", "") >= (time.strftime("%Y-%m-%dT%H:%M", time.gmtime(time.time() - 120)))]
    check(len(newest) == 1, "the restore executed exactly once", f"{len(newest)} restore operation(s) in the last 2 minutes")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base", required=True)
    ap.add_argument("--state", default="./smoke-state.json")
    ap.add_argument("--insecure", action="store_true", help="skip TLS verification (local self-signed stack only)")
    ap.add_argument("--host-header", default="", help="send this Host header (local test via 127.0.0.1)")
    ap.add_argument("--node-name", default="nodera-staging-01")
    ap.add_argument("--project", default="")
    ap.add_argument("phase", choices=["bootstrap", "security", "provision", "approval-race"])
    args = ap.parse_args()
    st = load(args.state)
    c = Client(args.base, args.insecure, args.host_header)
    {"bootstrap": bootstrap, "security": security, "provision": provision, "approval-race": approval_race}[args.phase](c, st, args)
    print(f"\n{len(FAILED)} failed" if FAILED else "\nall checks passed")
    sys.exit(1 if FAILED else 0)


if __name__ == "__main__":
    main()
