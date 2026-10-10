# Site migration engine

Moves an existing WordPress site into a Nodera WordPress project. The live site is **never touched
before an approved cutover**.

```
SOURCE → PREFLIGHT → CONFIGURATION → TRANSFER → VALIDATION → CUTOVER → COMPLETE
```

| Stage | Operation | What happens |
|---|---|---|
| plan | `migration.plan` | discovery + preflight → `PASS / WARNING / BLOCKER` report. No side effects. Status `planned` or `preflight_failed`. |
| run | `migration.run` | extract into a **staging** area, create a staging database, import the dump with URLs rewritten, validate, compute the Migration Health Score (0–100). Status `ready_for_cutover`. Failure removes staging and leaves the live site as it was. |
| cutover | `migration.cutover` — **approval required** (Tool Gateway) | safety backup (full, verified) → swap files → swap database → restart → health check. Any failure restores the safety backup automatically. |
| rollback | `migration.rollback` | restores the pre-cutover safety backup after a completed migration. |

## Sources

| Kind | State |
|---|---|
| `zip`, `updraftplus`, `manual` | **implemented**: an uploaded archive containing the WordPress files and a `.sql` dump (max 64 MiB through the API) |
| `ftp`, `sftp`, `ssh`, `cpanel`, `wordpress` | **interface only** (`sitemig.Connector`). No connector ships enabled; preflight reports the honest BLOCKER *"no connector is available…"* instead of pretending to connect. Needs the remote protocol clients and credentials. |

## Safety properties (all tested)

* Zip-slip / `..` / absolute / backslash entries make the archive unusable (preflight BLOCKER); extraction is bounded by
  *actual* bytes (zip-bomb guard), symlinks are skipped.
* `wp-config.php` is **never** carried over (it holds the old server's credentials; the target container is configured from its own environment).
* URL rewriting operates on SQL string literals only and **recomputes PHP-serialized lengths** (`s:N:"…"`), including values nested inside serialized
  strings and JSON-escaped URLs; comments, identifiers and non-literal text are untouched.
* Credentials are stored encrypted through the secrets service, never in `source_config`, logs or audit; keys that look like secrets are rejected in `source_config`.
* Preflight blockers: not WordPress, multisite, no database dump, no target project, missing DB/FS provider. Warnings: cache/security plugins, PHP version, size, custom `wp-config.php` settings.
* One active migration per target domain (partial unique index).

## Health score

Files transferred (30) + database imported (25) + no remaining references to the old URL (20) + WordPress core (10) + plugins/themes (10) + new URL present (5); capped at 40 with any blocker. Cutover refuses a score below 50.

## Not verified here

Remote connectors, very large archives (>64 MiB via the API), and DNS cutover at a real registrar.
