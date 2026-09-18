"use client";

import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { StatusBadge } from "@/components/StatusBadge";
import type { Application } from "@/lib/types";

export default function ApplicationsPage() {
  const apps = useApi(() => api.get<Application[]>("/api/v1/applications"), []);
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [kind, setKind] = useState("service");
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

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

      <div className="card">
        {apps.loading ? (
          <div className="p-4 text-sm text-base-400">Loading…</div>
        ) : (apps.data ?? []).length === 0 ? (
          <div className="p-4 text-sm text-base-400">No applications registered yet.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Kind</th>
                <th>Environment</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {apps.data!.map((a) => (
                <tr key={a.id}>
                  <td className="font-mono">{a.name}</td>
                  <td>{a.kind}</td>
                  <td>{a.environment}</td>
                  <td>
                    <StatusBadge status={a.status} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
