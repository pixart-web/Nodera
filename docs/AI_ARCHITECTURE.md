# AI Architecture

Status: **IMPLEMENTED**, including two real production-capable provider
adapters — one local (Ollama), one cloud (Anthropic). The profile registry,
provider/model registry, the deterministic router (including enforced
privacy-level policy), gateway `/api/v1/ai/*` HTTP surface, and usage
tracking are real, tested code — see `internal/ai/ai.go`,
`internal/ai/registry.go`, and `internal/integration_test.go` /
`internal/ollama_integration_test.go` / `internal/anthropic_integration_test.go`.
See [ADR-006](DECISIONS.md#adr-006-ai-gateway-agent-runtime-and-tool-gateway-ship-as-interfaces--deterministic-stubs-in-phase-1).

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

- **`ai_providers` / `ai_models`** (model registry): provider-agnostic,
  **platform-wide** records (no `organization_id` — see the note on
  `internal/ai/registry.go`) of what's available, where (cloud vs. local,
  which node), and its capabilities/cost/context window. Managed through
  `POST /api/v1/ai/providers` / `POST /api/v1/ai/models` (`ai.manage`) —
  registering a row here does not, by itself, make a provider callable;
  see Provider adapters below. Seeded with `local-echo`/`echo-1` (a
  deliberately fake test provider, so tests have something real to
  exercise without faking production AI behavior — rule 36).
- **`ai_profiles`**: named, versioned configuration ("infrastructure.analysis")
  that callers request instead of a specific model — required capabilities,
  privacy level, preferred/fallback model IDs, temperature, token limits,
  retry policy.
- **Routing**: profile → policy check (privacy level) → eligible models →
  deterministic selection → provider adapter call (`Service.resolve` in
  `internal/ai/ai.go`). Section 13 of the brief explicitly asks for a
  **deterministic** router first, not ML-based routing — that's what this
  is; cost/latency/quality-aware selection remains future work.
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

## Provider adapters

A provider's `ai_providers` row (the registry) and its Go adapter (a
`providers.Provider` implementation, registered at process startup in
`cmd/server/main.go`) are two separate things on purpose — a row can exist
with no adapter wired (e.g. an operator pre-registers it, or a different
process handles that provider), and `resolve` treats that as unavailable,
never a fabricated call (rule 36; see
`TestAIRegistryProviderWithNoAdapterIsUnavailable`).

- **`local-echo`** (`internal/ai/providers/localecho`): deterministic test
  provider, always registered, never presented as production.
- **`ollama`** (`internal/ai/providers/ollama`): a real adapter for an
  Ollama-compatible local inference server's `/api/chat` endpoint. Chosen
  as the first production adapter because it needs no cloud credential to
  develop or run against (rule 39: no GPU/huge-model requirement for normal
  development — the adapter is tested against a mock HTTP server, not a
  real Ollama install). Registered only when `NODERA_OLLAMA_BASE_URL` is
  set; `cmd/server/main.go` then also auto-registers the `ollama` row in
  the registry so it's discoverable via `GET /api/v1/ai/providers`. A
  model still has to be registered separately (`POST /api/v1/ai/models`)
  once you know which model(s) your Ollama instance actually serves —
  Nodera has no way to introspect that automatically yet.
- **`anthropic`** (`internal/ai/providers/anthropic`): a real adapter for
  the Anthropic Messages API (`kind: cloud`, so `restricted`-privacy
  profiles correctly refuse to route to it — verified by
  `TestAIRestrictedProfileNeverRoutesToAnthropic`). Registered only when
  `NODERA_ANTHROPIC_API_KEY` is set. Anthropic's request shape differs from
  Ollama's in one structural way the adapter handles: a system prompt is a
  separate top-level `system` field, not a `"system"`-role message —
  `Chat` extracts and joins any `"system"`-role messages out of the
  request before sending it, rather than passing them through and having
  Anthropic reject the call. Tested against a mock HTTP server, not a real
  Anthropic account (rule 39) — no test anywhere in the codebase depends
  on `NODERA_ANTHROPIC_API_KEY` being set.

## Credential handling for cloud providers

`NODERA_ANTHROPIC_API_KEY` is sourced from an environment variable, not
`internal/secrets`, even though the secrets module already exists and is
exactly the kind of thing it's for. The reason is a real, not-yet-resolved
architectural mismatch: `ai_providers`/`ai_models` are platform-wide (no
`organization_id` — see `internal/ai/registry.go`), while `internal/secrets`
is org-scoped by design (every secret belongs to one organization,
`docs/SECURITY.md`). There's no organization to scope a platform-wide
provider's credential to. Resolving this — likely a small "platform
secrets" concept distinct from org secrets — is tracked in
`docs/ROADMAP.md` rather than worked around silently.

## What's deliberately not built yet

- Further cloud provider adapters (OpenAI, etc.) — the registry, router,
  and gateway are provider-agnostic and ready for one; the Ollama → Anthropic
  order was deliberate (no-credential-needed adapter proven first)
- Cost/latency/quality-aware routing (section 13 explicitly asks for the
  deterministic router first, which is what exists)
- Embeddings/RAG (`docs/ARCHITECTURE.md` §7 sketches the shape; no code yet)
- Prompt/response content logging (deliberately out of scope by default —
  see the Usage tracking section above)
- Auto-discovery of models an Ollama instance actually has pulled, or
  models an Anthropic API key has access to
- A "platform secrets" mechanism for cloud provider credentials, resolving
  the org-scoped-secrets-vs-platform-wide-provider mismatch noted above

These are prioritized in `docs/ROADMAP.md`.
