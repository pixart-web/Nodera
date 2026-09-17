# AI Architecture

Status: **IMPLEMENTED**, backed only by the `local-echo` test provider — no
production provider adapter exists yet (that part stays **FOUNDATION
ONLY**). The profile registry, deterministic router (including enforced
privacy-level policy), gateway `/api/v1/ai/*` HTTP surface, and usage
tracking are real, tested code — see `internal/ai/ai.go` and
`internal/integration_test.go`'s `TestAIProfileCreateAndChatRoundTrip`. See
[ADR-006](DECISIONS.md#adr-006-ai-gateway-agent-runtime-and-tool-gateway-ship-as-interfaces--deterministic-stubs-in-phase-1).

## Why applications never call a vendor SDK directly

Every Nodera-ecosystem product (CyberAudit, Web Content Flow, Kiko,
SearchAnvil, ...) is meant to call **Nodera's** `/api/v1/ai/*` surface, never
OpenAI/Anthropic/Ollama directly. That's what makes it possible to swap,
combine, or locally-host models later without touching every consuming
product — see the guiding principle in the product brief (§49).

## Shape

```
caller → AI Profile → routing.Router → providers.Provider adapter → usage record
```

- **`ai_providers` / `ai_models`** (model registry): provider-agnostic
  records of what's available, where (cloud vs. local, which node), and its
  capabilities/cost/context window. Seeded today with a single `local-echo`
  test provider and `echo-1` test model — deliberately not a real model, so
  integration tests and the router have something real to exercise without
  faking production AI behavior (rule 36).
- **`ai_profiles`**: named, versioned configuration ("infrastructure.analysis")
  that callers request instead of a specific model — required capabilities,
  privacy level, preferred/fallback model IDs, temperature, token limits,
  retry policy.
- **Routing** (not yet implemented): profile → policy check (privacy level)
  → eligible models → deterministic selection → provider adapter call.
  Section 13 of the brief explicitly asks for a **deterministic** router
  first, not ML-based routing — that's the plan here too.
- **Privacy classification** (`public`/`internal`/`confidential`/`restricted`
  on `ai_profiles.privacy_level`): a `restricted` profile must never resolve
  to a `kind = 'cloud'` provider. This has to be enforced in the router, not
  left to the caller — a caller cannot request its way around the policy.
- **Usage tracking** (`ai_usage_records`): operational metadata only —
  tokens, latency, cost, provider/model, local-vs-cloud classification,
  success/failure. No prompt or response content is persisted here by
  design (rule 15); if content-level logging is ever needed it will be a
  separate, explicitly-retention-policied table, not bolted onto this one.

## Model references

An `ai_profiles` row lists preferred/fallback models as human-readable
strings, `"<provider_key>/<model_identifier>"` (e.g. `"local-echo/echo-1"`),
rather than a model UUID — so a profile can be authored via the API without
first looking one up. `Service.resolve` (`internal/ai/ai.go`) walks
preferred then fallback refs, skipping any that don't exist yet, aren't
`active`/`available`, fail the privacy check, or have no registered Go
adapter — never fabricating a match (rule 36). If nothing resolves, `Chat`
returns `UNAVAILABLE`.

## What's deliberately not built yet

- Real provider adapters (OpenAI, Anthropic, Ollama, vLLM, ...) — the
  registry, router, and gateway are provider-agnostic and ready for one
- Cost/latency/quality-aware routing (section 13 explicitly asks for the
  deterministic router first, which is what exists)
- Embeddings/RAG (`docs/ARCHITECTURE.md` §7 sketches the shape; no code yet)
- Prompt/response content logging (deliberately out of scope by default —
  see the Usage tracking section above)

These are prioritized in `docs/ROADMAP.md`.
