# AI Architecture

Status: **FOUNDATION ONLY**. Schema (`0006_ai.sql`) and the architectural
shape below are real; there is no HTTP surface, router implementation, or
production provider adapter yet. See [ADR-006](DECISIONS.md#adr-006-ai-gateway-agent-runtime-and-tool-gateway-ship-as-interfaces--deterministic-stubs-in-phase-1).

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

## What's deliberately not built yet

- Real provider adapters (OpenAI, Anthropic, Ollama, vLLM, ...)
- The `/api/v1/ai/*` HTTP surface
- The router's actual selection algorithm
- Embeddings/RAG (`docs/ARCHITECTURE.md` §7 sketches the shape; no code yet)

These are prioritized in `docs/ROADMAP.md`.
