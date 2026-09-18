-- check_ssl now has a real execution backend (a genuine TLS handshake,
-- internal/tools/handlers/checkssl.go — registered in cmd/server/main.go)
-- so, like get_server_metrics before it (migration 0010), it's no longer
-- accurate to advertise it as implemented=false. See docs/AGENTS.md.
UPDATE tools SET implemented = true WHERE key = 'check_ssl';
