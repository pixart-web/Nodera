"use client";

import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { StatusBadge } from "@/components/StatusBadge";
import type { Node as NoderaNode, Page } from "@/lib/types";

export default function InfrastructurePage() {
  const [visibleLimit, setVisibleLimit] = useState(50);
  const nodes = useApi(
    () => api.get<Page<NoderaNode>>(`/api/v1/infrastructure/nodes?limit=${visibleLimit}`),
    [visibleLimit],
  );
  const [showForm, setShowForm] = useState(false);
  const [hostname, setHostname] = useState("");
  const [provider, setProvider] = useState("");
  const [role, setRole] = useState("application");
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function registerNode(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    setBusy(true);
    try {
      await api.post("/api/v1/infrastructure/nodes", { hostname, provider, role });
      setHostname("");
      setProvider("");
      setShowForm(false);
      nodes.reload();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Failed to register node");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <div className="mb-6 flex items-center justify-between">
        <PageHeader title="Infrastructure" description="Provider-agnostic node inventory." />
        <button className="btn-primary" onClick={() => setShowForm((v) => !v)}>
          {showForm ? "Cancel" : "Register node"}
        </button>
      </div>

      {showForm && (
        <form onSubmit={registerNode} className="card mb-6 space-y-3 p-4">
          {formError && <ErrorBanner message={formError} />}
          <div className="grid grid-cols-3 gap-3">
            <div>
              <label className="label" htmlFor="hostname">
                Hostname
              </label>
              <input
                id="hostname"
                className="input"
                value={hostname}
                onChange={(e) => setHostname(e.target.value)}
                placeholder="nodera-prod-01"
                required
              />
            </div>
            <div>
              <label className="label" htmlFor="provider">
                Provider
              </label>
              <input
                id="provider"
                className="input"
                value={provider}
                onChange={(e) => setProvider(e.target.value)}
                placeholder="hetzner, local, aws…"
              />
            </div>
            <div>
              <label className="label" htmlFor="role">
                Role
              </label>
              <select id="role" className="input" value={role} onChange={(e) => setRole(e.target.value)}>
                <option value="application">application</option>
                <option value="database">database</option>
                <option value="storage">storage</option>
                <option value="worker">worker</option>
                <option value="ai-inference">ai-inference</option>
                <option value="monitoring">monitoring</option>
              </select>
            </div>
          </div>
          <button type="submit" className="btn-primary" disabled={busy}>
            {busy ? "Registering…" : "Register"}
          </button>
        </form>
      )}

      {nodes.error && <ErrorBanner message={nodes.error} />}

      <div className="card">
        {nodes.loading ? (
          <div className="p-4 text-sm text-base-400">Loading…</div>
        ) : (nodes.data?.items ?? []).length === 0 ? (
          <div className="p-4 text-sm text-base-400">
            No nodes registered yet. This is real inventory, not a placeholder — register your first node above.
          </div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Hostname</th>
                <th>Provider</th>
                <th>Role</th>
                <th>Environment</th>
                <th>Status</th>
                <th>Capabilities</th>
              </tr>
            </thead>
            <tbody>
              {nodes.data!.items.map((n) => (
                <tr key={n.id}>
                  <td className="font-mono">{n.hostname}</td>
                  <td>{n.provider}</td>
                  <td>{n.role}</td>
                  <td>{n.environment}</td>
                  <td>
                    <StatusBadge status={n.status} />
                  </td>
                  <td className="text-xs text-base-400">{n.capabilities.join(", ") || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {nodes.data?.has_more && (
        <button className="btn-secondary mt-3" onClick={() => setVisibleLimit((n) => n + 50)}>
          Load more
        </button>
      )}
    </div>
  );
}
