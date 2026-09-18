"use client";

import { Fragment, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { StatusBadge } from "@/components/StatusBadge";
import type { Approval, ExecuteResult, OrganizationToolSetting, Tool } from "@/lib/types";

const DEFAULT_APPROVAL_TTL_SECONDS = 24 * 60 * 60;

function formatTTL(seconds: number): string {
  if (seconds % 86400 === 0) return `${seconds / 86400}d`;
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

function ApprovalTTLCell({
  tool,
  override,
  onChanged,
}: {
  tool: Tool;
  override: OrganizationToolSetting | undefined;
  onChanged: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [minutes, setMinutes] = useState(String(Math.round((override?.approval_ttl_seconds ?? DEFAULT_APPROVAL_TTL_SECONDS) / 60)));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (tool.risk_level !== "privileged" && tool.risk_level !== "critical") {
    return <span className="text-xs text-base-500">—</span>;
  }

  async function save() {
    setError(null);
    setBusy(true);
    try {
      await api.put(`/api/v1/tools/${tool.key}/approval-ttl`, { approval_ttl_seconds: Number(minutes) * 60 });
      setEditing(false);
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to set approval TTL");
    } finally {
      setBusy(false);
    }
  }

  async function clear() {
    setError(null);
    setBusy(true);
    try {
      await api.del(`/api/v1/tools/${tool.key}/approval-ttl`);
      setEditing(false);
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to clear approval TTL override");
    } finally {
      setBusy(false);
    }
  }

  if (!editing) {
    return (
      <div className="flex items-center gap-2">
        <span className="text-xs text-base-300">
          {override ? formatTTL(override.approval_ttl_seconds) : `${formatTTL(DEFAULT_APPROVAL_TTL_SECONDS)} (default)`}
        </span>
        <button className="text-xs text-accent-400 hover:text-accent-300" onClick={() => setEditing(true)}>
          Edit
        </button>
      </div>
    );
  }

  return (
    <div className="space-y-1">
      {error && <div className="text-xs text-danger">{error}</div>}
      <div className="flex items-center gap-2">
        <input
          className="input w-20 py-1 text-xs"
          type="number"
          min={5}
          value={minutes}
          onChange={(e) => setMinutes(e.target.value)}
        />
        <span className="text-xs text-base-400">min</span>
        <button className="text-xs text-ok hover:underline" disabled={busy} onClick={save}>
          Save
        </button>
        {override && (
          <button className="text-xs text-base-400 hover:text-danger" disabled={busy} onClick={clear}>
            Clear
          </button>
        )}
        <button className="text-xs text-base-500 hover:text-base-300" onClick={() => setEditing(false)}>
          Cancel
        </button>
      </div>
    </div>
  );
}

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
  const approvalTTLOverrides = useApi(() => api.get<OrganizationToolSetting[]>("/api/v1/tools/approval-ttl"), []);
  const [openTool, setOpenTool] = useState<string | null>(null);

  const [statusFilter, setStatusFilter] = useState("pending");
  const approvals = useApi(
    () => api.get<Approval[]>(`/api/v1/approvals${statusFilter ? `?status=${statusFilter}` : ""}`),
    [statusFilter],
    { pollMs: 7000 },
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
                <th>Approval TTL</th>
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
                      <ApprovalTTLCell
                        tool={t}
                        override={approvalTTLOverrides.data?.find((o) => o.tool_key === t.key)}
                        onChanged={() => approvalTTLOverrides.reload()}
                      />
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
                      <td colSpan={7} className="bg-base-800/40 p-3">
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
        <div>
          <h2 className="text-sm font-medium text-base-100">Approvals</h2>
          <p className="text-xs text-base-500">Refreshes automatically every 7s.</p>
        </div>
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
