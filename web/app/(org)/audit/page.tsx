"use client";

import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import type { AuditRecord } from "@/lib/types";

export default function AuditPage() {
  const audit = useApi(() => api.get<AuditRecord[]>("/api/v1/audit"), []);

  return (
    <div>
      <PageHeader title="Audit log" description="Append-only. Every sensitive operation is recorded here." />

      {audit.error && <ErrorBanner message={audit.error} />}

      <div className="card">
        {audit.loading ? (
          <div className="p-4 text-sm text-base-400">Loading…</div>
        ) : (audit.data ?? []).length === 0 ? (
          <div className="p-4 text-sm text-base-400">No audit records yet.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Resource</th>
                <th>Actor</th>
                <th>Source</th>
                <th>Result</th>
                <th>When</th>
              </tr>
            </thead>
            <tbody>
              {audit.data!.map((a) => (
                <tr key={a.id}>
                  <td className="font-mono text-xs">{a.action}</td>
                  <td className="text-xs text-base-300">
                    {a.resource_type}
                    {a.resource_id ? `:${a.resource_id.slice(0, 8)}` : ""}
                  </td>
                  <td className="text-xs text-base-300">{a.actor_label}</td>
                  <td className="text-xs text-base-400">{a.source}</td>
                  <td className={`text-xs ${a.success ? "text-ok" : "text-danger"}`}>
                    {a.success ? "success" : "failed"}
                  </td>
                  <td className="text-xs text-base-400">{new Date(a.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
