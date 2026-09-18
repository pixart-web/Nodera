"use client";

import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { StatusBadge } from "@/components/StatusBadge";
import type { AIModel, AIProfile, AIProvider, ChatResult } from "@/lib/types";

function parseList(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

function ChatPanel({ profiles }: { profiles: AIProfile[] }) {
  const [profileKey, setProfileKey] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<ChatResult | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setResult(null);
    setBusy(true);
    try {
      const res = await api.post<ChatResult>("/api/v1/ai/chat", {
        profile_key: profileKey,
        messages: [{ role: "user", content: message }],
      });
      setResult(res);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to reach the AI gateway");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      <div>
        <label className="label" htmlFor="chat-profile">
          Profile
        </label>
        <select
          id="chat-profile"
          className="input"
          value={profileKey}
          onChange={(e) => setProfileKey(e.target.value)}
          required
        >
          <option value="" disabled>
            Select a profile…
          </option>
          {profiles.map((p) => (
            <option key={p.key} value={p.key}>
              {p.key} ({p.privacy_level})
            </option>
          ))}
        </select>
      </div>
      <div>
        <label className="label" htmlFor="chat-message">
          Message
        </label>
        <textarea
          id="chat-message"
          className="input"
          rows={3}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          required
        />
      </div>
      <button type="submit" className="btn-primary" disabled={busy || profiles.length === 0}>
        {busy ? "Sending…" : "Send"}
      </button>
      {profiles.length === 0 && <p className="text-xs text-base-400">Create a profile below first.</p>}
      {result && (
        <div className="rounded-md border border-base-600 bg-base-950 p-3 text-xs">
          <div className="whitespace-pre-wrap text-base-200">{result.content}</div>
          <div className="mt-2 text-base-500">
            {result.provider_key} / {result.model} — {result.input_tokens} in / {result.output_tokens} out tokens
          </div>
        </div>
      )}
    </form>
  );
}

function CreateProfileForm({ onCreated }: { onCreated: () => void }) {
  const [key, setKey] = useState("");
  const [description, setDescription] = useState("");
  const [privacyLevel, setPrivacyLevel] = useState("internal");
  const [preferredModelIds, setPreferredModelIds] = useState("");
  const [fallbackModelIds, setFallbackModelIds] = useState("");
  const [requiredCapabilities, setRequiredCapabilities] = useState("");
  const [temperature, setTemperature] = useState("0.7");
  const [maxTokens, setMaxTokens] = useState("2048");
  const [timeoutSeconds, setTimeoutSeconds] = useState("60");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.post<AIProfile>("/api/v1/ai/profiles", {
        key,
        description,
        privacy_level: privacyLevel,
        preferred_model_ids: parseList(preferredModelIds),
        fallback_model_ids: parseList(fallbackModelIds),
        required_capabilities: parseList(requiredCapabilities),
        temperature: Number(temperature),
        max_tokens: Number(maxTokens),
        timeout_seconds: Number(timeoutSeconds),
      });
      setKey("");
      setDescription("");
      setPreferredModelIds("");
      setFallbackModelIds("");
      setRequiredCapabilities("");
      onCreated();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create profile");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card mb-4 space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="label" htmlFor="profile-key">
            Key
          </label>
          <input id="profile-key" className="input font-mono" value={key} onChange={(e) => setKey(e.target.value)} required />
        </div>
        <div>
          <label className="label" htmlFor="profile-privacy">
            Privacy level
          </label>
          <select
            id="profile-privacy"
            className="input"
            value={privacyLevel}
            onChange={(e) => setPrivacyLevel(e.target.value)}
          >
            <option value="public">public</option>
            <option value="internal">internal</option>
            <option value="confidential">confidential</option>
            <option value="restricted">restricted</option>
          </select>
        </div>
      </div>
      <div>
        <label className="label" htmlFor="profile-description">
          Description
        </label>
        <input id="profile-description" className="input" value={description} onChange={(e) => setDescription(e.target.value)} />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="label" htmlFor="profile-preferred">
            Preferred model ids (comma-separated, provider_key/model_identifier)
          </label>
          <input
            id="profile-preferred"
            className="input font-mono"
            value={preferredModelIds}
            onChange={(e) => setPreferredModelIds(e.target.value)}
            placeholder="local-echo/echo-1"
            required
          />
        </div>
        <div>
          <label className="label" htmlFor="profile-fallback">
            Fallback model ids (comma-separated)
          </label>
          <input
            id="profile-fallback"
            className="input font-mono"
            value={fallbackModelIds}
            onChange={(e) => setFallbackModelIds(e.target.value)}
          />
        </div>
      </div>
      <div>
        <label className="label" htmlFor="profile-capabilities">
          Required capabilities (comma-separated)
        </label>
        <input
          id="profile-capabilities"
          className="input font-mono"
          value={requiredCapabilities}
          onChange={(e) => setRequiredCapabilities(e.target.value)}
        />
      </div>
      <div className="grid grid-cols-3 gap-3">
        <div>
          <label className="label" htmlFor="profile-temp">
            Temperature
          </label>
          <input
            id="profile-temp"
            className="input"
            type="number"
            step="0.1"
            value={temperature}
            onChange={(e) => setTemperature(e.target.value)}
          />
        </div>
        <div>
          <label className="label" htmlFor="profile-max-tokens">
            Max tokens
          </label>
          <input
            id="profile-max-tokens"
            className="input"
            type="number"
            value={maxTokens}
            onChange={(e) => setMaxTokens(e.target.value)}
          />
        </div>
        <div>
          <label className="label" htmlFor="profile-timeout">
            Timeout (seconds)
          </label>
          <input
            id="profile-timeout"
            className="input"
            type="number"
            value={timeoutSeconds}
            onChange={(e) => setTimeoutSeconds(e.target.value)}
          />
        </div>
      </div>
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Creating…" : "Create profile"}
      </button>
    </form>
  );
}

function RegisterProviderForm({ onDone }: { onDone: () => void }) {
  const [key, setKey] = useState("");
  const [kind, setKind] = useState("cloud");
  const [displayName, setDisplayName] = useState("");
  const [status, setStatus] = useState("active");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.post<AIProvider>("/api/v1/ai/providers", { key, kind, display_name: displayName, status });
      setKey("");
      setDisplayName("");
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to register provider");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card mb-4 space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      <div className="grid grid-cols-2 gap-3">
        <input className="input font-mono" placeholder="key" value={key} onChange={(e) => setKey(e.target.value)} required />
        <input
          className="input"
          placeholder="Display name"
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          required
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <select className="input" value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="cloud">cloud</option>
          <option value="local">local</option>
        </select>
        <select className="input" value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="active">active</option>
          <option value="disabled">disabled</option>
          <option value="unconfigured">unconfigured</option>
          <option value="unavailable">unavailable</option>
        </select>
      </div>
      <p className="text-xs text-base-400">
        This registers a discoverable row only — whether the provider is actually callable depends on a matching Go
        adapter being registered at server boot (see docs/AI_ARCHITECTURE.md).
      </p>
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Saving…" : "Register / update provider"}
      </button>
    </form>
  );
}

function RegisterModelForm({ providers, onDone }: { providers: AIProvider[]; onDone: () => void }) {
  const [providerKey, setProviderKey] = useState("");
  const [modelIdentifier, setModelIdentifier] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [capabilities, setCapabilities] = useState("");
  const [contextWindow, setContextWindow] = useState("8192");
  const [status, setStatus] = useState("available");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.post<AIModel>("/api/v1/ai/models", {
        provider_key: providerKey,
        model_identifier: modelIdentifier,
        display_name: displayName,
        capabilities: parseList(capabilities),
        context_window: Number(contextWindow),
        status,
      });
      setModelIdentifier("");
      setDisplayName("");
      setCapabilities("");
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to register model");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card mb-4 space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      <div className="grid grid-cols-2 gap-3">
        <select className="input" value={providerKey} onChange={(e) => setProviderKey(e.target.value)} required>
          <option value="" disabled>
            Select a provider…
          </option>
          {providers.map((p) => (
            <option key={p.key} value={p.key}>
              {p.key}
            </option>
          ))}
        </select>
        <input
          className="input font-mono"
          placeholder="model_identifier"
          value={modelIdentifier}
          onChange={(e) => setModelIdentifier(e.target.value)}
          required
        />
      </div>
      <input
        className="input"
        placeholder="Display name"
        value={displayName}
        onChange={(e) => setDisplayName(e.target.value)}
        required
      />
      <div className="grid grid-cols-3 gap-3">
        <input
          className="input font-mono"
          placeholder="capabilities (comma-separated)"
          value={capabilities}
          onChange={(e) => setCapabilities(e.target.value)}
        />
        <input
          className="input"
          type="number"
          placeholder="context_window"
          value={contextWindow}
          onChange={(e) => setContextWindow(e.target.value)}
        />
        <select className="input" value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="available">available</option>
          <option value="unavailable">unavailable</option>
          <option value="deprecated">deprecated</option>
        </select>
      </div>
      <button type="submit" className="btn-primary" disabled={busy || providers.length === 0}>
        {busy ? "Saving…" : "Register / update model"}
      </button>
      {providers.length === 0 && <p className="text-xs text-base-400">Register a provider first.</p>}
    </form>
  );
}

export default function AIPage() {
  const profiles = useApi(() => api.get<AIProfile[]>("/api/v1/ai/profiles"), []);
  const providers = useApi(() => api.get<AIProvider[]>("/api/v1/ai/providers"), []);
  const models = useApi(() => api.get<AIModel[]>("/api/v1/ai/models"), []);

  const [showProfileForm, setShowProfileForm] = useState(false);
  const [showProviderForm, setShowProviderForm] = useState(false);
  const [showModelForm, setShowModelForm] = useState(false);

  return (
    <div>
      <PageHeader
        title="AI Gateway"
        description="Deterministic, privacy-policy-enforcing routing. Providers/models are a platform-wide registry — a row here is only discoverable, not necessarily callable (that depends on a Go adapter registered at server boot)."
      />

      <div className="mb-8">
        <h2 className="mb-3 text-sm font-medium text-base-100">Chat</h2>
        {profiles.error && <ErrorBanner message={profiles.error} />}
        {!profiles.loading && <ChatPanel profiles={profiles.data ?? []} />}
      </div>

      <div className="mb-8">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-medium text-base-100">Profiles</h2>
          <button className="btn-primary" onClick={() => setShowProfileForm((v) => !v)}>
            {showProfileForm ? "Cancel" : "Create profile"}
          </button>
        </div>
        {showProfileForm && (
          <CreateProfileForm
            onCreated={() => {
              setShowProfileForm(false);
              profiles.reload();
            }}
          />
        )}
        {profiles.error && <ErrorBanner message={profiles.error} />}
        <div className="card">
          {profiles.loading ? (
            <div className="p-4 text-sm text-base-400">Loading…</div>
          ) : (profiles.data ?? []).length === 0 ? (
            <div className="p-4 text-sm text-base-400">No AI profiles yet.</div>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Key</th>
                  <th>Privacy</th>
                  <th>Preferred models</th>
                  <th>Fallback models</th>
                  <th>Max tokens</th>
                </tr>
              </thead>
              <tbody>
                {profiles.data!.map((p) => (
                  <tr key={p.key}>
                    <td className="font-mono text-xs">{p.key}</td>
                    <td>
                      <span className="badge bg-base-500/20 text-base-300">{p.privacy_level}</span>
                    </td>
                    <td className="font-mono text-xs text-base-300">{p.preferred_model_ids.join(", ")}</td>
                    <td className="font-mono text-xs text-base-400">{p.fallback_model_ids.join(", ") || "—"}</td>
                    <td className="text-xs text-base-400">{p.max_tokens}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div className="mb-8">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-medium text-base-100">Providers</h2>
          <button className="btn-primary" onClick={() => setShowProviderForm((v) => !v)}>
            {showProviderForm ? "Cancel" : "Register provider"}
          </button>
        </div>
        {showProviderForm && (
          <RegisterProviderForm
            onDone={() => {
              setShowProviderForm(false);
              providers.reload();
            }}
          />
        )}
        {providers.error && <ErrorBanner message={providers.error} />}
        <div className="card">
          {providers.loading ? (
            <div className="p-4 text-sm text-base-400">Loading…</div>
          ) : (providers.data ?? []).length === 0 ? (
            <div className="p-4 text-sm text-base-400">No providers registered.</div>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Key</th>
                  <th>Kind</th>
                  <th>Display name</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {providers.data!.map((p) => (
                  <tr key={p.key}>
                    <td className="font-mono text-xs">{p.key}</td>
                    <td className="text-xs text-base-300">{p.kind}</td>
                    <td className="text-xs text-base-300">{p.display_name}</td>
                    <td>
                      <StatusBadge status={p.status} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-medium text-base-100">Models</h2>
          <button className="btn-primary" onClick={() => setShowModelForm((v) => !v)}>
            {showModelForm ? "Cancel" : "Register model"}
          </button>
        </div>
        {showModelForm && (
          <RegisterModelForm
            providers={providers.data ?? []}
            onDone={() => {
              setShowModelForm(false);
              models.reload();
            }}
          />
        )}
        {models.error && <ErrorBanner message={models.error} />}
        <div className="card">
          {models.loading ? (
            <div className="p-4 text-sm text-base-400">Loading…</div>
          ) : (models.data ?? []).length === 0 ? (
            <div className="p-4 text-sm text-base-400">No models registered.</div>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Provider</th>
                  <th>Identifier</th>
                  <th>Display name</th>
                  <th>Context window</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {models.data!.map((m) => (
                  <tr key={`${m.provider_key}/${m.model_identifier}`}>
                    <td className="font-mono text-xs">{m.provider_key}</td>
                    <td className="font-mono text-xs text-base-300">{m.model_identifier}</td>
                    <td className="text-xs text-base-300">{m.display_name}</td>
                    <td className="text-xs text-base-400">{m.context_window}</td>
                    <td>
                      <StatusBadge status={m.status} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}
