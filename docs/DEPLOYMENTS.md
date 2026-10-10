# Deployment engine

`PRECHECK → FETCH → BUILD → TEST → DEPLOY → HEALTH_CHECK → COMPLETE`, persisted as operation steps with live logs.

* **Sources:** `github` / `git` (through `GitProvider`) and `upload` (base64 files in the request, ≤ 20 000 files / 200 MB).
  The Git provider is an interface: `mock` serves a fixture repo; a real GitHub adapter needs a token and is **not** shipped yet
  (`docs/ROADMAP.md`). Without a Git provider, git deployments are refused with a clear message; uploads work.
* **No command execution.** `BUILD` is recorded as *skipped* unless a real builder exists — the pipeline never claims a build ran.
* **Releases.** Each deployment is stored under `projects/<slug>/releases/<id>`; the live tree is `projects/<slug>/app`.
  The previous live tree is preserved during `DEPLOY`; any later failure (including `HEALTH_CHECK`) restores it automatically
  (the failed deployment is marked `failed`, the project keeps serving the old version). The last 5 releases are kept.
* **Rollback** (`deployment.rollback`) restores a previous release as a new deployment row.
* **Production needs approval.** `deployment.deploy` is for non-production environments; `deployment.production` can only be requested
  through the Tool Gateway (`Submit` refuses it), so nothing reaches production before a human approves.
* **One active deployment per project/environment** (partial unique index).
* Inputs are validated: repository `owner/name`, git ref charset, environment name, upload paths (no `..`, absolute, backslash, empty segments).

Not implemented: webhooks (`CreateWebhook` exists on the provider interface), build containers, blue/green traffic switching.
