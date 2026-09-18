"use client";

import { Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { StatusBadge } from "@/components/StatusBadge";
import type { Job } from "@/lib/types";

export default function JobsPage() {
  return (
    <Suspense fallback={<div className="text-sm text-base-400">Loading…</div>}>
      <JobsPageInner />
    </Suspense>
  );
}

function JobsPageInner() {
  const searchParams = useSearchParams();
  const statusFilter = searchParams.get("status") ?? "";

  const jobs = useApi(
    () => api.get<Job[]>(`/api/v1/jobs${statusFilter ? `?status=${statusFilter}` : ""}`),
    [statusFilter],
  );

  const [showForm, setShowForm] = useState(false);
  const [type, setType] = useState("");
  const [payload, setPayload] = useState("{}");
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function enqueue(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    let parsedPayload: unknown;
    try {
      parsedPayload = JSON.parse(payload);
    } catch {
      setFormError("Payload must be valid JSON");
      return;
    }
    setBusy(true);
    try {
      await api.post("/api/v1/jobs", { type, payload: parsedPayload });
      setType("");
      setPayload("{}");
      setShowForm(false);
      jobs.reload();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Failed to enqueue job");
    } finally {
      setBusy(false);
    }
  }

  async function cancelJob(id: string) {
    try {
      await api.post(`/api/v1/jobs/${id}/cancel`);
      jobs.reload();
    } catch {
      // The job list will simply not reflect a cancellation that failed
      // (e.g. it already started running) — reload shows the true state.
      jobs.reload();
    }
  }

  return (
    <div>
      <div className="mb-6 flex items-center justify-between">
        <PageHeader
          title="Jobs"
          description="Postgres-backed job queue. No job type has a handler registered yet beyond what callers enqueue — see docs/ROADMAP.md."
        />
        <button className="btn-primary" onClick={() => setShowForm((v) => !v)}>
          {showForm ? "Cancel" : "Enqueue job"}
        </button>
      </div>

      {showForm && (
        <form onSubmit={enqueue} className="card mb-6 space-y-3 p-4">
          {formError && <ErrorBanner message={formError} />}
          <div>
            <label className="label" htmlFor="job-type">
              Type
            </label>
            <input
              id="job-type"
              className="input"
              value={type}
              onChange={(e) => setType(e.target.value)}
              placeholder="e.g. backup.create"
              required
            />
          </div>
          <div>
            <label className="label" htmlFor="job-payload">
              Payload (JSON)
            </label>
            <textarea
              id="job-payload"
              className="input font-mono"
              rows={3}
              value={payload}
              onChange={(e) => setPayload(e.target.value)}
            />
          </div>
          <button type="submit" className="btn-primary" disabled={busy}>
            {busy ? "Enqueuing…" : "Enqueue"}
          </button>
        </form>
      )}

      {jobs.error && <ErrorBanner message={jobs.error} />}

      <div className="card">
        {jobs.loading ? (
          <div className="p-4 text-sm text-base-400">Loading…</div>
        ) : (jobs.data ?? []).length === 0 ? (
          <div className="p-4 text-sm text-base-400">No jobs{statusFilter ? ` with status "${statusFilter}"` : ""}.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Type</th>
                <th>Status</th>
                <th>Attempts</th>
                <th>Created</th>
                <th>Error</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {jobs.data!.map((j) => (
                <tr key={j.id}>
                  <td className="font-mono text-xs">{j.type}</td>
                  <td>
                    <StatusBadge status={j.status} />
                  </td>
                  <td className="text-xs text-base-300">
                    {j.attempts}/{j.max_attempts}
                  </td>
                  <td className="text-xs text-base-400">{new Date(j.created_at).toLocaleString()}</td>
                  <td className="max-w-xs truncate text-xs text-danger">{j.error ?? ""}</td>
                  <td>
                    {j.status === "queued" && (
                      <button className="text-xs text-base-400 hover:text-danger" onClick={() => cancelJob(j.id)}>
                        Cancel
                      </button>
                    )}
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
