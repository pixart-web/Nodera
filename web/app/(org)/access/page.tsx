"use client";

import { Fragment, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { StatusBadge } from "@/components/StatusBadge";
import type { AdminAPIToken, APIToken, CreatedAPIToken, ServiceAccount } from "@/lib/types";

function parseScopes(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

// Shown once, right after a token is minted — the API never returns the raw
// value again after this response (only its hash is stored).
function NewTokenBanner({ token, onDismiss }: { token: string; onDismiss: () => void }) {
  return (
    <div className="mb-4 rounded-md border border-ok/30 bg-ok/10 px-3 py-3 text-sm">
      <div className="mb-1 font-medium text-ok">Token created — copy it now, it won&apos;t be shown again</div>
      <code className="block break-all rounded bg-base-950 px-2 py-1 font-mono text-xs text-base-100">{token}</code>
      <button className="mt-2 text-xs text-base-400 hover:text-base-200" onClick={onDismiss}>
        Dismiss
      </button>
    </div>
  );
}

export default function AccessPage() {
  const myTokens = useApi(() => api.get<APIToken[]>("/api/v1/api-tokens"), []);
  const serviceAccounts = useApi(() => api.get<ServiceAccount[]>("/api/v1/service-accounts"), []);
  // Org-wide token visibility requires organization.manage — a member
  // without it gets FORBIDDEN, which we treat as "section not available"
  // rather than an error to show (see the .catch below).
  const orgTokens = useApi(
    () => api.get<AdminAPIToken[]>("/api/v1/organization/api-tokens").catch((e) => {
      if (e instanceof ApiError && e.code === "FORBIDDEN") return null;
      throw e;
    }),
    [],
  );

  const [newToken, setNewToken] = useState<string | null>(null);

  const [showTokenForm, setShowTokenForm] = useState(false);
  const [tokenName, setTokenName] = useState("");
  const [tokenScopes, setTokenScopes] = useState("");
  const [tokenFormError, setTokenFormError] = useState<string | null>(null);
  const [tokenBusy, setTokenBusy] = useState(false);

  async function createToken(e: React.FormEvent) {
    e.preventDefault();
    setTokenFormError(null);
    setTokenBusy(true);
    try {
      const created = await api.post<CreatedAPIToken>("/api/v1/api-tokens", {
        name: tokenName,
        scopes: parseScopes(tokenScopes),
      });
      setNewToken(created.token);
      setTokenName("");
      setTokenScopes("");
      setShowTokenForm(false);
      myTokens.reload();
    } catch (err) {
      setTokenFormError(err instanceof ApiError ? err.message : "Failed to create token");
    } finally {
      setTokenBusy(false);
    }
  }

  async function revokeToken(id: string) {
    try {
      await api.del(`/api/v1/api-tokens/${id}`);
      myTokens.reload();
    } catch {
      myTokens.reload();
    }
  }

  const [renamingTokenID, setRenamingTokenID] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [renameError, setRenameError] = useState<string | null>(null);
  const [renameBusy, setRenameBusy] = useState(false);

  function startRenaming(t: APIToken) {
    setRenamingTokenID((cur) => (cur === t.id ? null : t.id));
    setRenameValue(t.name);
    setRenameError(null);
  }

  async function saveRename(e: React.FormEvent, id: string) {
    e.preventDefault();
    setRenameError(null);
    setRenameBusy(true);
    try {
      await api.put(`/api/v1/api-tokens/${id}`, { name: renameValue });
      setRenamingTokenID(null);
      myTokens.reload();
      orgTokens.reload();
    } catch (err) {
      setRenameError(err instanceof ApiError ? err.message : "Failed to rename token");
    } finally {
      setRenameBusy(false);
    }
  }

  async function revokeOrgToken(id: string) {
    try {
      await api.del(`/api/v1/organization/api-tokens/${id}`);
      orgTokens.reload();
    } catch {
      orgTokens.reload();
    }
  }

  const [showSAForm, setShowSAForm] = useState(false);
  const [saName, setSaName] = useState("");
  const [saDescription, setSaDescription] = useState("");
  const [saFormError, setSaFormError] = useState<string | null>(null);
  const [saBusy, setSaBusy] = useState(false);

  async function createServiceAccount(e: React.FormEvent) {
    e.preventDefault();
    setSaFormError(null);
    setSaBusy(true);
    try {
      await api.post("/api/v1/service-accounts", { name: saName, description: saDescription });
      setSaName("");
      setSaDescription("");
      setShowSAForm(false);
      serviceAccounts.reload();
    } catch (err) {
      setSaFormError(err instanceof ApiError ? err.message : "Failed to create service account");
    } finally {
      setSaBusy(false);
    }
  }

  async function disableServiceAccount(id: string) {
    try {
      await api.del(`/api/v1/service-accounts/${id}`);
      serviceAccounts.reload();
      orgTokens.reload();
    } catch {
      serviceAccounts.reload();
    }
  }

  const [saStatusError, setSaStatusError] = useState<string | null>(null);

  async function enableServiceAccount(id: string) {
    setSaStatusError(null);
    try {
      await api.post(`/api/v1/service-accounts/${id}/enable`);
      serviceAccounts.reload();
    } catch (err) {
      setSaStatusError(err instanceof ApiError ? err.message : "Failed to enable service account");
    }
  }

  const [deletingSAID, setDeletingSAID] = useState<string | null>(null);

  async function deleteServiceAccount(id: string) {
    setSaStatusError(null);
    setDeletingSAID(id);
    try {
      await api.del(`/api/v1/service-accounts/${id}/permanent`);
      serviceAccounts.reload();
    } catch (err) {
      setSaStatusError(err instanceof ApiError ? err.message : "Failed to delete service account");
    } finally {
      setDeletingSAID(null);
    }
  }

  const [editingSAID, setEditingSAID] = useState<string | null>(null);
  const [editSAName, setEditSAName] = useState("");
  const [editSADescription, setEditSADescription] = useState("");
  const [editSAError, setEditSAError] = useState<string | null>(null);
  const [editSABusy, setEditSABusy] = useState(false);

  function startEditingSA(sa: ServiceAccount) {
    setEditingSAID((cur) => (cur === sa.id ? null : sa.id));
    setEditSAName(sa.name);
    setEditSADescription(sa.description);
    setEditSAError(null);
  }

  async function saveServiceAccount(e: React.FormEvent, id: string) {
    e.preventDefault();
    setEditSAError(null);
    setEditSABusy(true);
    try {
      await api.put(`/api/v1/service-accounts/${id}`, { name: editSAName, description: editSADescription });
      setEditingSAID(null);
      serviceAccounts.reload();
    } catch (err) {
      setEditSAError(err instanceof ApiError ? err.message : "Failed to update service account");
    } finally {
      setEditSABusy(false);
    }
  }

  const [issuingFor, setIssuingFor] = useState<string | null>(null);
  const [saTokenName, setSaTokenName] = useState("");
  const [saTokenScopes, setSaTokenScopes] = useState("");
  const [saTokenError, setSaTokenError] = useState<string | null>(null);
  const [saTokenBusy, setSaTokenBusy] = useState(false);

  async function issueServiceAccountToken(e: React.FormEvent, saId: string) {
    e.preventDefault();
    setSaTokenError(null);
    setSaTokenBusy(true);
    try {
      const created = await api.post<CreatedAPIToken>(`/api/v1/service-accounts/${saId}/api-tokens`, {
        name: saTokenName,
        scopes: parseScopes(saTokenScopes),
      });
      setNewToken(created.token);
      setSaTokenName("");
      setSaTokenScopes("");
      setIssuingFor(null);
      orgTokens.reload();
    } catch (err) {
      setSaTokenError(err instanceof ApiError ? err.message : "Failed to issue token");
    } finally {
      setSaTokenBusy(false);
    }
  }

  return (
    <div>
      <PageHeader
        title="Access"
        description="API tokens and service accounts. Scopes can never exceed the granting user's own permissions."
      />

      {newToken && <NewTokenBanner token={newToken} onDismiss={() => setNewToken(null)} />}

      {/* --- My API tokens --- */}
      <div className="mb-8">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-medium text-base-100">My API tokens</h2>
          <button className="btn-primary" onClick={() => setShowTokenForm((v) => !v)}>
            {showTokenForm ? "Cancel" : "Create token"}
          </button>
        </div>

        {showTokenForm && (
          <form onSubmit={createToken} className="card mb-4 space-y-3 p-4">
            {tokenFormError && <ErrorBanner message={tokenFormError} />}
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="label" htmlFor="token-name">
                  Name
                </label>
                <input id="token-name" className="input" value={tokenName} onChange={(e) => setTokenName(e.target.value)} required />
              </div>
              <div>
                <label className="label" htmlFor="token-scopes">
                  Scopes (comma-separated)
                </label>
                <input
                  id="token-scopes"
                  className="input font-mono"
                  value={tokenScopes}
                  onChange={(e) => setTokenScopes(e.target.value)}
                  placeholder="infrastructure.read, audit.read"
                  required
                />
              </div>
            </div>
            <button type="submit" className="btn-primary" disabled={tokenBusy}>
              {tokenBusy ? "Creating…" : "Create"}
            </button>
          </form>
        )}

        {myTokens.error && <ErrorBanner message={myTokens.error} />}
        <div className="card">
          {myTokens.loading ? (
            <div className="p-4 text-sm text-base-400">Loading…</div>
          ) : (myTokens.data ?? []).length === 0 ? (
            <div className="p-4 text-sm text-base-400">No API tokens yet.</div>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Prefix</th>
                  <th>Scopes</th>
                  <th>Last used</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {myTokens.data!.map((t) => (
                  <Fragment key={t.id}>
                    <tr>
                      <td className="font-mono text-xs">{t.name}</td>
                      <td className="font-mono text-xs text-base-400">{t.token_prefix}…</td>
                      <td className="text-xs text-base-300">{t.scopes.join(", ")}</td>
                      <td className="text-xs text-base-400">{t.last_used_at ? new Date(t.last_used_at).toLocaleString() : "never"}</td>
                      <td className="space-x-3">
                        <button className="text-xs text-base-400 hover:text-base-200" onClick={() => startRenaming(t)}>
                          {renamingTokenID === t.id ? "Close" : "Rename"}
                        </button>
                        <button className="text-xs text-base-400 hover:text-danger" onClick={() => revokeToken(t.id)}>
                          Revoke
                        </button>
                      </td>
                    </tr>
                    {renamingTokenID === t.id && (
                      <tr>
                        <td colSpan={5} className="bg-base-800/40 p-3">
                          <form onSubmit={(e) => saveRename(e, t.id)} className="flex items-center gap-3">
                            {renameError && <ErrorBanner message={renameError} />}
                            <input
                              className="input flex-1"
                              value={renameValue}
                              onChange={(e) => setRenameValue(e.target.value)}
                              required
                            />
                            <button type="submit" className="btn-primary" disabled={renameBusy}>
                              {renameBusy ? "Saving…" : "Save"}
                            </button>
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
      </div>

      {/* --- Service accounts --- */}
      <div className="mb-8">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-medium text-base-100">Service accounts</h2>
          <button className="btn-primary" onClick={() => setShowSAForm((v) => !v)}>
            {showSAForm ? "Cancel" : "Create service account"}
          </button>
        </div>

        {showSAForm && (
          <form onSubmit={createServiceAccount} className="card mb-4 space-y-3 p-4">
            {saFormError && <ErrorBanner message={saFormError} />}
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="label" htmlFor="sa-name">
                  Name
                </label>
                <input id="sa-name" className="input" value={saName} onChange={(e) => setSaName(e.target.value)} required />
              </div>
              <div>
                <label className="label" htmlFor="sa-description">
                  Description
                </label>
                <input id="sa-description" className="input" value={saDescription} onChange={(e) => setSaDescription(e.target.value)} />
              </div>
            </div>
            <button type="submit" className="btn-primary" disabled={saBusy}>
              {saBusy ? "Creating…" : "Create"}
            </button>
          </form>
        )}

        {serviceAccounts.error && <ErrorBanner message={serviceAccounts.error} />}
        {saStatusError && <ErrorBanner message={saStatusError} />}
        <div className="card">
          {serviceAccounts.loading ? (
            <div className="p-4 text-sm text-base-400">Loading…</div>
          ) : (serviceAccounts.data ?? []).length === 0 ? (
            <div className="p-4 text-sm text-base-400">No service accounts yet.</div>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Description</th>
                  <th>Status</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {serviceAccounts.data!.map((sa) => (
                  <Fragment key={sa.id}>
                    <tr>
                      <td className="font-mono">{sa.name}</td>
                      <td className="text-xs text-base-300">{sa.description || "—"}</td>
                      <td>
                        <StatusBadge status={sa.status} />
                      </td>
                      <td className="space-x-3">
                        {sa.status === "active" ? (
                          <>
                            <button
                              className="text-xs text-accent-400 hover:text-accent-300"
                              onClick={() => setIssuingFor(issuingFor === sa.id ? null : sa.id)}
                            >
                              {issuingFor === sa.id ? "Cancel" : "Issue token"}
                            </button>
                            <button className="text-xs text-base-400 hover:text-base-200" onClick={() => startEditingSA(sa)}>
                              {editingSAID === sa.id ? "Close" : "Edit"}
                            </button>
                            <button className="text-xs text-base-400 hover:text-danger" onClick={() => disableServiceAccount(sa.id)}>
                              Disable
                            </button>
                          </>
                        ) : (
                          <>
                            <button className="text-xs text-base-400 hover:text-base-200" onClick={() => startEditingSA(sa)}>
                              {editingSAID === sa.id ? "Close" : "Edit"}
                            </button>
                            <button
                              className="text-xs text-accent-400 hover:text-accent-300"
                              onClick={() => enableServiceAccount(sa.id)}
                            >
                              Enable
                            </button>
                            <button
                              className="text-xs text-base-400 hover:text-danger disabled:text-base-600"
                              disabled={deletingSAID === sa.id}
                              onClick={() => deleteServiceAccount(sa.id)}
                            >
                              {deletingSAID === sa.id ? "Deleting…" : "Delete"}
                            </button>
                          </>
                        )}
                      </td>
                    </tr>
                    {issuingFor === sa.id && (
                      <tr>
                        <td colSpan={4} className="bg-base-800/40 p-3">
                          <form onSubmit={(e) => issueServiceAccountToken(e, sa.id)} className="space-y-2">
                            {saTokenError && <ErrorBanner message={saTokenError} />}
                            <div className="grid grid-cols-2 gap-3">
                              <input
                                className="input"
                                placeholder="Token name"
                                value={saTokenName}
                                onChange={(e) => setSaTokenName(e.target.value)}
                                required
                              />
                              <input
                                className="input font-mono"
                                placeholder="Scopes, comma-separated"
                                value={saTokenScopes}
                                onChange={(e) => setSaTokenScopes(e.target.value)}
                                required
                              />
                            </div>
                            <button type="submit" className="btn-primary" disabled={saTokenBusy}>
                              {saTokenBusy ? "Issuing…" : "Issue"}
                            </button>
                          </form>
                        </td>
                      </tr>
                    )}
                    {editingSAID === sa.id && (
                      <tr>
                        <td colSpan={4} className="bg-base-800/40 p-3">
                          <form onSubmit={(e) => saveServiceAccount(e, sa.id)} className="space-y-2">
                            {editSAError && <ErrorBanner message={editSAError} />}
                            <div className="grid grid-cols-2 gap-3">
                              <input
                                className="input"
                                placeholder="Name"
                                value={editSAName}
                                onChange={(e) => setEditSAName(e.target.value)}
                                required
                              />
                              <input
                                className="input"
                                placeholder="Description"
                                value={editSADescription}
                                onChange={(e) => setEditSADescription(e.target.value)}
                              />
                            </div>
                            <div className="flex items-center gap-3">
                              <button type="submit" className="btn-primary" disabled={editSABusy}>
                                {editSABusy ? "Saving…" : "Save changes"}
                              </button>
                              <button
                                type="button"
                                className="text-xs text-base-400 hover:text-base-200"
                                onClick={() => setEditingSAID(null)}
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
      </div>

      {/* --- Organization-wide token visibility (organization.manage only) --- */}
      {orgTokens.data && (
        <div>
          <h2 className="mb-3 text-sm font-medium text-base-100">All organization tokens</h2>
          <div className="card">
            {orgTokens.data.length === 0 ? (
              <div className="p-4 text-sm text-base-400">No tokens in this organization.</div>
            ) : (
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Owner</th>
                    <th>Scopes</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {orgTokens.data.map((t) => (
                    <tr key={t.id}>
                      <td className="font-mono text-xs">{t.name}</td>
                      <td className="text-xs text-base-300">
                        {t.owner_label} <span className="text-base-500">({t.owner_type})</span>
                      </td>
                      <td className="text-xs text-base-300">{t.scopes.join(", ")}</td>
                      <td>
                        <button className="text-xs text-base-400 hover:text-danger" onClick={() => revokeOrgToken(t.id)}>
                          Revoke
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
