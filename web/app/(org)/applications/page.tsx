"use client";

import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { StatusBadge } from "@/components/StatusBadge";
import type { Application, Page } from "@/lib/types";

export default function ApplicationsPage() {
  const [visibleLimit, setVisibleLimit] = useState(50);
  const apps = useApi(() => api.get<Page<Application>>(`/api/v1/applications?limit=${visibleLimit}`), [visibleLimit]);
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [kind, setKind] = useState("service");
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [rowError, setRowError] = useState<string | null>(null);

  async function reportStatus(id: string, status: string) {
    setRowError(null);
    try {
      await api.post(`/api/v1/applications/${id}/status`, { status });
      apps.reload();
    } catch (err) {
      setRowError(err instanceof ApiError ? err.message : "Failed to update application status");
    }
  }

  async function deregister(id: string) {
    setRowError(null);
    try {
      await api.post(`/api/v1/applications/${id}/deregister`);
      apps.reload();
    } catch (err) {
      setRowError(err instanceof ApiError ? err.message : "Failed to deregister application");
    }
  }

  async function registerApp(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    setBusy(true);
    try {
      await api.post("/api/v1/applications", { name, kind });
      setName("");
      setShowForm(false);
      apps.reload();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Failed to register application");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <div className="mb-6 flex items-center justify-between">
        <PageHeader title="Applications" description="Registered applications and services." />
        <button className="btn-primary" onClick={() => setShowForm((v) => !v)}>
          {showForm ? "Cancel" : "Register application"}
        </button>
      </div>

      {showForm && (
        <form onSubmit={registerApp} className="card mb-6 space-y-3 p-4">
          {formError && <ErrorBanner message={formError} />}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="label" htmlFor="app-name">
                Name
              </label>
              <input
                id="app-name"
                className="input"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
              />
            </div>
            <div>
              <label className="label" htmlFor="app-kind">
                Kind
              </label>
              <select id="app-kind" className="input" value={kind} onChange={(e) => setKind(e.target.value)}>
                <option value="service">service</option>
                <option value="web">web</option>
                <option value="worker">worker</option>
                <option value="job">job</option>
                <option value="database">database</option>
              </select>
            </div>
          </div>
          <button type="submit" className="btn-primary" disabled={busy}>
            {busy ? "Registering…" : "Register"}
          </button>
        </form>
      )}

      {apps.error && <ErrorBanner message={apps.error} />}
      {rowError && <ErrorBanner message={rowError} />}

      <div className="card">
        {apps.loading ? (
          <div className="p-4 text-sm text-base-400">Loading…</div>
        ) : (apps.data?.items ?? []).length === 0 ? (
          <div className="p-4 text-sm text-base-400">No applications registered yet.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Kind</th>
                <th>Environment</th>
                <th>Status</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {apps.data!.items.map((a) => (
                <tr key={a.id}>
                  <td className="font-mono">{a.name}</td>
                  <td>{a.kind}</td>
                  <td>{a.environment}</td>
                  <td>
                    <StatusBadge status={a.status} />
                  </td>
                  <td className="space-x-2">
                    {a.status !== "deregistered" && (
                      <>
                        <select
                          className="input inline-block w-28 py-1 text-xs"
                          value=""
                          onChange={(e) => e.target.value && reportStatus(a.id, e.target.value)}
                        >
                          <option value="" disabled>
                            Set status…
                          </option>
                          <option value="running">running</option>
                          <option value="stopped">stopped</option>
                          <option value="degraded">degraded</option>
                          <option value="failed">failed</option>
                          <option value="unknown">unknown</option>
                        </select>
                        <button
                          className="text-xs text-base-400 hover:text-danger"
                          onClick={() => deregister(a.id)}
                        >
                          Deregister
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {apps.data?.has_more && (
        <button className="btn-secondary mt-3" onClick={() => setVisibleLimit((n) => n + 50)}>
          Load more
        </button>
      )}
    </div>
  );
}
