"use client";

import { Fragment, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { StatusBadge } from "@/components/StatusBadge";
import type { Agent, ChatResult, ExecuteResult } from "@/lib/types";

function parseList(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

function CreateAgentForm({ onCreated }: { onCreated: () => void }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [systemInstructions, setSystemInstructions] = useState("");
  const [aiProfileKey, setAiProfileKey] = useState("");
  const [allowedToolKeys, setAllowedToolKeys] = useState("");
  const [permissionScope, setPermissionScope] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.post<Agent>("/api/v1/agents", {
        name,
        description,
        system_instructions: systemInstructions,
        ai_profile_key: aiProfileKey,
        allowed_tool_keys: parseList(allowedToolKeys),
        permission_scope: parseList(permissionScope),
      });
      setName("");
      setDescription("");
      setSystemInstructions("");
      setAiProfileKey("");
      setAllowedToolKeys("");
      setPermissionScope("");
      onCreated();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create agent");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card mb-4 space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="label" htmlFor="agent-name">
            Name
          </label>
          <input id="agent-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div>
          <label className="label" htmlFor="agent-profile">
            AI profile key
          </label>
          <input
            id="agent-profile"
            className="input font-mono"
            value={aiProfileKey}
            onChange={(e) => setAiProfileKey(e.target.value)}
            placeholder="e.g. ops.assistant"
            required
          />
        </div>
      </div>
      <div>
        <label className="label" htmlFor="agent-description">
          Description
        </label>
        <input id="agent-description" className="input" value={description} onChange={(e) => setDescription(e.target.value)} />
      </div>
      <div>
        <label className="label" htmlFor="agent-instructions">
          System instructions
        </label>
        <textarea
          id="agent-instructions"
          className="input font-mono"
          rows={2}
          value={systemInstructions}
          onChange={(e) => setSystemInstructions(e.target.value)}
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="label" htmlFor="agent-tools">
            Allowed tool keys (comma-separated)
          </label>
          <input
            id="agent-tools"
            className="input font-mono"
            value={allowedToolKeys}
            onChange={(e) => setAllowedToolKeys(e.target.value)}
            placeholder="check_ssl, get_server_metrics"
          />
        </div>
        <div>
          <label className="label" htmlFor="agent-scope">
            Permission scope (comma-separated)
          </label>
          <input
            id="agent-scope"
            className="input font-mono"
            value={permissionScope}
            onChange={(e) => setPermissionScope(e.target.value)}
            placeholder="ai.use, tools.read"
          />
        </div>
      </div>
      <p className="text-xs text-base-400">
        permission_scope can never exceed your own held permissions — the API rejects anything broader (no
        privilege escalation). A new agent starts disabled.
      </p>
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Creating…" : "Create agent"}
      </button>
    </form>
  );
}

function RunAgentForm({ agent, onDone }: { agent: Agent; onDone: () => void }) {
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
      const res = await api.post<ChatResult>(`/api/v1/agents/${agent.id}/run`, { message });
      setResult(res);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to run agent");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-2">
      {error && <ErrorBanner message={error} />}
      <textarea
        className="input"
        rows={2}
        placeholder="Message"
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        required
      />
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Running…" : "Send"}
      </button>
      {result && (
        <div className="rounded-md border border-base-600 bg-base-950 p-2 text-xs">
          <div className="whitespace-pre-wrap text-base-200">{result.content}</div>
          <div className="mt-1 text-base-500">
            via {result.provider_key} / {result.model}
          </div>
        </div>
      )}
      <div className="pt-1">
        <button type="button" className="text-xs text-base-400 hover:text-base-200" onClick={onDone}>
          Close
        </button>
      </div>
    </form>
  );
}

function ExecuteAgentToolForm({ agent, onDone }: { agent: Agent; onDone: () => void }) {
  const [toolKey, setToolKey] = useState(agent.allowed_tool_keys[0] ?? "");
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
      const res = await api.post<ExecuteResult>(`/api/v1/agents/${agent.id}/tools/${toolKey}/execute`, {
        resource_type: resourceType,
        resource_id: resourceID,
        parameters: parsedParams,
      });
      setResult(res);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to execute tool");
    } finally {
      setBusy(false);
    }
  }

  if (agent.allowed_tool_keys.length === 0) {
    return <p className="text-xs text-base-400">This agent has no allowed_tool_keys configured.</p>;
  }

  return (
    <form onSubmit={submit} className="space-y-2">
      {error && <ErrorBanner message={error} />}
      <div className="grid grid-cols-3 gap-3">
        <select className="input" value={toolKey} onChange={(e) => setToolKey(e.target.value)}>
          {agent.allowed_tool_keys.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
        <input
          className="input"
          placeholder="resource_type"
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
            <span className="text-warn">Approval required (id: {result.approval_id}) — see the Tools page.</span>
          )}
        </div>
      )}
      <div className="pt-1">
        <button type="button" className="text-xs text-base-400 hover:text-base-200" onClick={onDone}>
          Close
        </button>
      </div>
    </form>
  );
}

type PanelKind = "run" | "execute";

export default function AgentsPage() {
  const agents = useApi(() => api.get<Agent[]>("/api/v1/agents"), []);
  const [showCreate, setShowCreate] = useState(false);
  const [openPanel, setOpenPanel] = useState<{ id: string; kind: PanelKind } | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);

  async function toggleStatus(agent: Agent) {
    setStatusError(null);
    try {
      const path = agent.status === "active" ? "disable" : "enable";
      await api.post(`/api/v1/agents/${agent.id}/${path}`);
      agents.reload();
    } catch (err) {
      setStatusError(err instanceof ApiError ? err.message : "Failed to change agent status");
    }
  }

  function togglePanel(id: string, kind: PanelKind) {
    setOpenPanel((cur) => (cur?.id === id && cur.kind === kind ? null : { id, kind }));
  }

  return (
    <div>
      <PageHeader
        title="Agents"
        description="Agent identity + scoped execution — not an autonomous tool-calling loop. An agent's own permission_scope and allowed_tool_keys bound its scoped chat (Run) and scoped tool execution (ExecuteTool); a caller always directs which tool an agent uses."
      />

      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-medium text-base-100">Agent definitions</h2>
        <button className="btn-primary" onClick={() => setShowCreate((v) => !v)}>
          {showCreate ? "Cancel" : "Create agent"}
        </button>
      </div>

      {showCreate && (
        <CreateAgentForm
          onCreated={() => {
            setShowCreate(false);
            agents.reload();
          }}
        />
      )}

      {agents.error && <ErrorBanner message={agents.error} />}
      {statusError && <ErrorBanner message={statusError} />}

      <div className="card">
        {agents.loading ? (
          <div className="p-4 text-sm text-base-400">Loading…</div>
        ) : (agents.data ?? []).length === 0 ? (
          <div className="p-4 text-sm text-base-400">No agents yet.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>AI profile</th>
                <th>Allowed tools</th>
                <th>Permission scope</th>
                <th>Status</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {agents.data!.map((a) => (
                <Fragment key={a.id}>
                  <tr>
                    <td className="font-mono text-xs">{a.name}</td>
                    <td className="font-mono text-xs text-base-400">{a.ai_profile_key}</td>
                    <td className="text-xs text-base-300">
                      {a.allowed_tool_keys.length > 0 ? a.allowed_tool_keys.join(", ") : "—"}
                    </td>
                    <td className="text-xs text-base-300">
                      {a.permission_scope.length > 0 ? a.permission_scope.join(", ") : "—"}
                    </td>
                    <td>
                      <StatusBadge status={a.status} />
                    </td>
                    <td className="space-x-3">
                      <button
                        className="text-xs text-accent-400 hover:text-accent-300 disabled:text-base-600"
                        disabled={a.status !== "active"}
                        onClick={() => togglePanel(a.id, "run")}
                      >
                        {openPanel?.id === a.id && openPanel.kind === "run" ? "Close" : "Run"}
                      </button>
                      <button
                        className="text-xs text-accent-400 hover:text-accent-300 disabled:text-base-600"
                        disabled={a.status !== "active"}
                        onClick={() => togglePanel(a.id, "execute")}
                      >
                        {openPanel?.id === a.id && openPanel.kind === "execute" ? "Close" : "Execute tool"}
                      </button>
                      <button className="text-xs text-base-400 hover:text-base-200" onClick={() => toggleStatus(a)}>
                        {a.status === "active" ? "Disable" : "Enable"}
                      </button>
                    </td>
                  </tr>
                  {openPanel?.id === a.id && (
                    <tr>
                      <td colSpan={6} className="bg-base-800/40 p-3">
                        {openPanel.kind === "run" ? (
                          <RunAgentForm agent={a} onDone={() => setOpenPanel(null)} />
                        ) : (
                          <ExecuteAgentToolForm agent={a} onDone={() => setOpenPanel(null)} />
                        )}
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
  );
}
