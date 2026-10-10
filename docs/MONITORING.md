# Monitoring, alerting, incidents, logs

* **Monitors:** `http`, `tcp`, `dns`, `ssl`, `container`, `database`. Checks run through the monitoring provider, which enforces the SSRF policy
  (`internal/platform/netpolicy`): loopback, private, link-local and metadata addresses (including bare IPv6 literals) are blocked unless
  `NODERA_MONITOR_ALLOW_INTERNAL=true` (development only). Due monitors are claimed with `FOR UPDATE SKIP LOCKED`.
* **Metrics:** node CPU/RAM/disk come from Node Agent heartbeats; `http_latency`, `ssl_days_remaining`, `container_unhealthy` from monitors.
  Without an agent there are no node samples and the UI says so (nothing is invented).
* **Alert rules:** `http_failure`, `container_unhealthy`, `database_unavailable`, `ssl_expiry_days`, `cpu_above`, `ram_above`, `disk_above`.
  A breach opens **exactly one** live incident per dedupe key (unique partial index), notifies once, and **auto-resolves** when the condition clears.
* **Incidents:** `open → acknowledged → investigating → resolved → closed` with a timeline; illegal transitions are 409.
* **Notifications:** per-user read state; channels `in_app` (always), `webhook` (SSRF-validated at creation *and* at send time, connection pinned to the
  resolved address, no redirects) and `email` (reports *"SMTP is not configured"* until an SMTP integration exists — it never pretends to send).
* **Logs:** `log_entries` are redacted on ingest (`internal/platform/redact`: passwords, tokens, bearer headers, URL credentials, private keys, cloud keys);
  operation logs are redacted too. Search escapes `LIKE` wildcards. Live container output is redacted on read.
* **Retention:** defaults 30 d (job logs, metrics, log entries) and 365 d (resolved incidents). The **audit log is only pruned with an explicit policy**
  and never below 90 days.
* **Realtime:** `GET /operations/{id}/stream` (SSE, resumable with `Last-Event-ID`) and `GET /events` (unread count + operation changes).
