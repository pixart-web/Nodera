-- get_server_metrics now has a real execution backend
-- (cmd/server/main.go registers a handler wrapping infrastructure.Service.Get)
-- so it's no longer accurate to advertise it as implemented=false. See
-- docs/AGENTS.md. Every other seeded tool remains implemented=false — this
-- is the first, deliberately minimal proof that the Tool Gateway pipeline
-- (permission check -> risk tier -> [approval] -> handler -> audit) works
-- end to end, not a claim that the rest of the tool catalog is built.
UPDATE tools SET implemented = true WHERE key = 'get_server_metrics';
