"use client";

import { Fragment, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { StatusBadge } from "@/components/StatusBadge";
import type { Approval, ExecuteResult, Tool } from "@/lib/types";

function riskBadgeColor(risk: Tool["risk_level"]): string {
  switch (risk) {
    case "read":
      return "bg-base-500/20 text-base-300";
    case "safe":
      return "bg-ok/15 text-ok";
    case "privileged":
      return "bg-warn/15 text-warn";
    case "critical":
      return "bg-danger/15 text-danger";
  }
}

function ExecuteForm({ tool, onDone }: { tool: Tool; onDone: () => void }) {
  const [resourceType, setResourceType] = useState("");
  const [resourceID, setResourceID] = useState("");
  const [params, setParams] = useState("{}");
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<ExecuteResult | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setResult(null);
    let parsedParams: unknown;
    try {
      parsedParams = params.trim() ? JSON.parse(params) : {};
    } catch {
      setError("Parameters must be valid JSON");
      return;
    }
    setBusy(true);
    try {
      const res = await api.post<ExecuteResult>(`/api/v1/tools/${tool.key}/execute`, {
        resource_type: resourceType,
        resource_id: resourceID,
        parameters: parsedParams,
      });
      setResult(res);
      if (res.status === "approval_required") onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to execute tool");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-2">
      {error && <ErrorBanner message={error} />}
      <div className="grid grid-cols-2 gap-3">
        <input
          className="input"
          placeholder="resource_type (e.g. node, domain)"
          value={resourceType}
          onChange={(e) => setResourceType(e.target.value)}
        />
        <input
          className="input font-mono"
          placeholder="resource_id"
          value={resourceID}
          onChange={(e) => setResourceID(e.target.value)}
        />
      </div>
      <textarea
        className="input font-mono"
        rows={2}
        placeholder="parameters (JSON)"
        value={params}
        onChange={(e) => setParams(e.target.value)}
      />
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Running…" : "Execute"}
      </button>
      {result && (
        <div className="rounded-md border border-base-600 bg-base-950 p-2 text-xs">
          {result.status === "executed" ? (
            <pre className="whitespace-pre-wrap break-all text-base-200">{JSON.stringify(result.result, null, 2)}</pre>
          ) : (
            <span className="text-warn">Approval required — see the Approvals section below (id: {result.approval_id}).</span>
          )}
        </div>
      )}
    </form>
  );
}

export default function ToolsPage() {
  const tools = useApi(() => api.get<Tool[]>("/api/v1/tools"), []);
  const [openTool, setOpenTool] = useState<string | null>(null);

  const [statusFilter, setStatusFilter] = useState("pending");
  const approvals = useApi(
    () => api.get<Approval[]>(`/api/v1/approvals${statusFilter ? `?status=${statusFilter}` : ""}`),
    [statusFilter],
  );
  const [decideError, setDecideError] = useState<string | null>(null);
  const [reasons, setReasons] = useState<Record<string, string>>({});

  async function decide(id: string, approve: boolean) {
    setDecideError(null);
    try {
      await api.post(`/api/v1/approvals/${id}/decide`, { approve, reason: reasons[id] ?? "" });
      approvals.reload();
    } catch (err) {
      setDecideError(err instanceof ApiError ? err.message : "Failed to record decision");
    }
  }

  return (
    <div>
      <PageHeader
        title="Tools"
        description="The Tool Gateway. Read/safe tools run immediately; privileged/critical tools create an approval instead — see the Approvals section below."
      />

      {tools.error && <ErrorBanner message={tools.error} />}
      <div className="card mb-8">
        {tools.loading ? (
          <div className="p-4 text-sm text-base-400">Loading…</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Key</th>
                <th>Description</th>
                <th>Risk</th>
                <th>Required permission</th>
                <th>Status</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {tools.data!.map((t) => (
                <Fragment key={t.key}>
                  <tr>
                    <td className="font-mono text-xs">{t.key}</td>
                    <td className="text-xs text-base-300">{t.description}</td>
                    <td>
                      <span className={`badge ${riskBadgeColor(t.risk_level)}`}>{t.risk_level}</span>
                    </td>
                    <td className="font-mono text-xs text-base-400">{t.required_permission}</td>
                    <td>
                      {t.implemented ? (
                        <span className="badge bg-ok/15 text-ok">implemented</span>
                      ) : (
                        <span className="badge bg-base-500/20 text-base-400">not implemented</span>
                      )}
                    </td>
                    <td>
                      <button
                        className="text-xs text-accent-400 hover:text-accent-300"
                        onClick={() => setOpenTool(openTool === t.key ? null : t.key)}
                      >
                        {openTool === t.key ? "Close" : "Try it"}
                      </button>
                    </td>
                  </tr>
                  {openTool === t.key && (
                    <tr>
                      <td colSpan={6} className="bg-base-800/40 p-3">
                        <ExecuteForm tool={t} onDone={() => approvals.reload()} />
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-medium text-base-100">Approvals</h2>
        <select className="input w-40" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          <option value="pending">pending</option>
          <option value="approved">approved</option>
          <option value="rejected">rejected</option>
          <option value="expired">expired</option>
          <option value="">all</option>
        </select>
      </div>

      {decideError && <ErrorBanner message={decideError} />}
      <div className="card">
        {approvals.loading ? (
          <div className="p-4 text-sm text-base-400">Loading…</div>
        ) : (approvals.data ?? []).length === 0 ? (
          <div className="p-4 text-sm text-base-400">No {statusFilter || ""} approvals.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Risk</th>
                <th>Resource</th>
                <th>Status</th>
                <th>Requested</th>
                {statusFilter === "pending" && <th>Decision</th>}
              </tr>
            </thead>
            <tbody>
              {approvals.data!.map((a) => (
                <tr key={a.id}>
                  <td className="font-mono text-xs">{a.requested_action}</td>
                  <td>
                    <span className={`badge ${riskBadgeColor(a.risk_level)}`}>{a.risk_level}</span>
                  </td>
                  <td className="text-xs text-base-300">
                    {a.resource_type}:{a.resource_id}
                  </td>
                  <td>
                    <StatusBadge status={a.status} />
                  </td>
                  <td className="text-xs text-base-400">{new Date(a.created_at).toLocaleString()}</td>
                  {statusFilter === "pending" && a.status === "pending" && (
                    <td className="space-x-2">
                      <input
                        className="input inline-block w-32 py-1 text-xs"
                        placeholder="reason"
                        value={reasons[a.id] ?? ""}
                        onChange={(e) => setReasons((r) => ({ ...r, [a.id]: e.target.value }))}
                      />
                      <button className="text-xs text-ok hover:underline" onClick={() => decide(a.id, true)}>
                        Approve
                      </button>
                      <button className="text-xs text-danger hover:underline" onClick={() => decide(a.id, false)}>
                        Reject
                      </button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
