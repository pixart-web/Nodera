"use client";

import { Fragment, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import type { SecretMeta } from "@/lib/types";

export default function SecretsPage() {
  const secrets = useApi(() => api.get<SecretMeta[]>("/api/v1/secrets"), []);
  const [showForm, setShowForm] = useState(false);
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [description, setDescription] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const unavailable = secrets.error?.includes("not configured");

  async function setSecret(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    setBusy(true);
    try {
      await api.put(`/api/v1/secrets/${encodeURIComponent(key)}`, { value, description });
      setKey("");
      setValue("");
      setDescription("");
      setShowForm(false);
      secrets.reload();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Failed to set secret");
    } finally {
      setBusy(false);
    }
  }

  async function deleteSecret(k: string) {
    try {
      await api.del(`/api/v1/secrets/${encodeURIComponent(k)}`);
      secrets.reload();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Failed to delete secret");
    }
  }

  const [editingKey, setEditingKey] = useState<string | null>(null);
  const [editDescription, setEditDescription] = useState("");
  const [editError, setEditError] = useState<string | null>(null);
  const [editBusy, setEditBusy] = useState(false);

  function startEditing(s: SecretMeta) {
    setEditingKey((cur) => (cur === s.key ? null : s.key));
    setEditDescription(s.description);
    setEditError(null);
  }

  async function saveDescription(e: React.FormEvent, k: string) {
    e.preventDefault();
    setEditError(null);
    setEditBusy(true);
    try {
      await api.patch(`/api/v1/secrets/${encodeURIComponent(k)}/description`, { description: editDescription });
      setEditingKey(null);
      secrets.reload();
    } catch (err) {
      setEditError(err instanceof ApiError ? err.message : "Failed to update description");
    } finally {
      setEditBusy(false);
    }
  }

  return (
    <div>
      <div className="mb-6 flex items-center justify-between">
        <PageHeader
          title="Secrets"
          description="Encrypted at rest (AES-256-GCM). Values are never shown here or anywhere else after they're set."
        />
        {!unavailable && (
          <button className="btn-primary" onClick={() => setShowForm((v) => !v)}>
            {showForm ? "Cancel" : "Add secret"}
          </button>
        )}
      </div>

      {unavailable && (
        <ErrorBanner message="The secrets module is not configured on this server (NODERA_SECRETS_ENCRYPTION_KEY unset)." />
      )}
      {!unavailable && secrets.error && <ErrorBanner message={secrets.error} />}
      {formError && <ErrorBanner message={formError} />}

      {showForm && (
        <form onSubmit={setSecret} className="card mb-6 space-y-3 p-4">
          <div>
            <label className="label" htmlFor="secret-key">
              Key
            </label>
            <input
              id="secret-key"
              className="input font-mono"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="ai_provider.openai.api_key"
              required
            />
          </div>
          <div>
            <label className="label" htmlFor="secret-value">
              Value
            </label>
            <input
              id="secret-value"
              type="password"
              className="input"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              required
            />
          </div>
          <div>
            <label className="label" htmlFor="secret-description">
              Description
            </label>
            <input
              id="secret-description"
              className="input"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>
          <button type="submit" className="btn-primary" disabled={busy}>
            {busy ? "Saving…" : "Save secret"}
          </button>
        </form>
      )}

      {!unavailable && (
        <div className="card">
          {secrets.loading ? (
            <div className="p-4 text-sm text-base-400">Loading…</div>
          ) : (secrets.data ?? []).length === 0 ? (
            <div className="p-4 text-sm text-base-400">No secrets stored yet.</div>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Key</th>
                  <th>Description</th>
                  <th>Updated</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {secrets.data!.map((s) => (
                  <Fragment key={s.id}>
                    <tr>
                      <td className="font-mono text-xs">{s.key}</td>
                      <td className="text-xs text-base-300">{s.description || "—"}</td>
                      <td className="text-xs text-base-400">{new Date(s.updated_at).toLocaleString()}</td>
                      <td className="space-x-3">
                        <button className="text-xs text-base-400 hover:text-base-200" onClick={() => startEditing(s)}>
                          {editingKey === s.key ? "Close" : "Edit description"}
                        </button>
                        <button
                          className="text-xs text-base-400 hover:text-danger"
                          onClick={() => deleteSecret(s.key)}
                        >
                          Delete
                        </button>
                      </td>
                    </tr>
                    {editingKey === s.key && (
                      <tr>
                        <td colSpan={4} className="bg-base-800/40 p-3">
                          <form onSubmit={(e) => saveDescription(e, s.key)} className="space-y-2">
                            {editError && <ErrorBanner message={editError} />}
                            <p className="text-xs text-base-400">
                              Only the description changes here — the secret&apos;s value is never resupplied or
                              rotated by this form.
                            </p>
                            <input
                              className="input"
                              value={editDescription}
                              onChange={(e) => setEditDescription(e.target.value)}
                              placeholder="Description"
                            />
                            <div className="flex items-center gap-3">
                              <button type="submit" className="btn-primary" disabled={editBusy}>
                                {editBusy ? "Saving…" : "Save description"}
                              </button>
                              <button
                                type="button"
                                className="text-xs text-base-400 hover:text-base-200"
                                onClick={() => setEditingKey(null)}
                              >
                                Cancel
                              </button>
                            </div>
                          </form>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}
