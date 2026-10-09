"use client";

import { useState } from "react";
import { Check, Clock, Play, ShieldCheck, X } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { useAction } from "@/lib/useAction";
import { PageHeader } from "@/components/ui/PageHeader";
import { Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Alert } from "@/components/ui/Feedback";
import { Badge } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { Modal } from "@/components/ui/Overlay";
import { Field, Input, Select } from "@/components/ui/Forms";
import { RowMenu } from "@/components/ui/RowMenu";
import { StatusBadge } from "@/components/StatusBadge";
import type { Approval, ExecuteResult, OrganizationToolSetting, Tool } from "@/lib/types";

const DEFAULT_TTL = 24 * 60 * 60;

function formatTTL(s: number): string {
  if (s % 86400 === 0) return `${s / 86400}d`;
  if (s % 3600 === 0) return `${s / 3600}h`;
  if (s % 60 === 0) return `${s / 60}m`;
  return `${s}s`;
}

const RISK: Record<Tool["risk_level"], "neutral" | "green" | "orange" | "red"> = { read: "neutral", safe: "green", privileged: "orange", critical: "red" };
type ToolRow = Tool & { id: string };
const isGated = (t: Tool) => t.risk_level === "privileged" || t.risk_level === "critical";

function ExecuteModal({ tool, onClose, onApproval }: { tool: Tool | null; onClose: () => void; onApproval: () => void }) {
  const action = useAction();
  const [resourceType, setResourceType] = useState(""); const [resourceID, setResourceID] = useState(""); const [params, setParams] = useState("{}");
  const [jsonError, setJsonError] = useState<string | null>(null);
  const [result, setResult] = useState<ExecuteResult | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!tool) return;
    setJsonError(null); setResult(null);
    let parsed: unknown;
    try { parsed = params.trim() ? JSON.parse(params) : {}; } catch { setJsonError("Os parâmetros têm de ser JSON válido"); return; }
    let res: ExecuteResult | null = null;
    const ok = await action.run(async () => { res = await api.post<ExecuteResult>(`/api/v1/tools/${tool.key}/execute`, { resource_type: resourceType, resource_id: resourceID, parameters: parsed }); }, undefined, "Não foi possível executar a tool");
    if (ok && res) { setResult(res); if ((res as ExecuteResult).status === "approval_required") onApproval(); }
  }
  function close() { setResult(null); setJsonError(null); action.setError(null); onClose(); }

  return (
    <Modal open={!!tool} onClose={close} size="lg" title={`Executar ${tool?.key ?? ""}`} description={tool ? `${tool.description} — risco ${tool.risk_level}${isGated(tool) ? " (cria um pedido de aprovação)" : ""}` : undefined}
      footer={<><Button onClick={close}>Fechar</Button><Button variant="primary" type="submit" form="exec-form" disabled={action.busy} icon={<Play className="h-4 w-4" />}>{action.busy ? "A executar…" : "Executar"}</Button></>}>
      <form id="exec-form" onSubmit={submit} className="space-y-4">
        {(action.error || jsonError) && <Alert tone="error">{jsonError ?? action.error}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="resource_type">{(id) => <Input id={id} placeholder="ex.: node, domain" value={resourceType} onChange={(e) => setResourceType(e.target.value)} />}</Field>
          <Field label="resource_id">{(id) => <Input id={id} className="font-mono" value={resourceID} onChange={(e) => setResourceID(e.target.value)} />}</Field>
        </div>
        <Field label="Parâmetros (JSON)">{(id) => <textarea id={id} className="input font-mono" rows={3} value={params} onChange={(e) => setParams(e.target.value)} />}</Field>
        {result && (result.status === "executed"
          ? <pre className="nd-scroll max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-nd bg-[#050C17] p-3 font-mono text-xs text-nd-text">{JSON.stringify(result.result, null, 2)}</pre>
          : <Alert tone="warning" title="Aprovação necessária">Pedido criado (id {result.approval_id}). Decide-o na secção de aprovações abaixo.</Alert>)}
      </form>
    </Modal>
  );
}

function TTLModal({ tool, override, onClose, onChanged }: { tool: Tool | null; override?: OrganizationToolSetting; onClose: () => void; onChanged: () => void }) {
  const action = useAction();
  const [minutes, setMinutes] = useState("");
  const current = String(Math.round((override?.approval_ttl_seconds ?? DEFAULT_TTL) / 60));
  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!tool) return;
    if (await action.run(() => api.put(`/api/v1/tools/${tool.key}/approval-ttl`, { approval_ttl_seconds: Number(minutes || current) * 60 }), "Validade da aprovação atualizada.")) { onChanged(); onClose(); }
  }
  async function clear() {
    if (!tool) return;
    if (await action.run(() => api.del(`/api/v1/tools/${tool.key}/approval-ttl`), "Voltou à validade por defeito.")) { onChanged(); onClose(); }
  }
  return (
    <Modal open={!!tool} onClose={onClose} size="sm" title="Validade da aprovação" description={`${tool?.key ?? ""} — entre 5 minutos e 30 dias. Só afeta pedidos futuros.`}
      footer={<>{override && <Button variant="danger" disabled={action.busy} onClick={clear} className="mr-auto">Repor por defeito</Button>}<Button onClick={onClose}>Cancelar</Button><Button variant="primary" type="submit" form="ttl-form" disabled={action.busy}>Guardar</Button></>}>
      <form id="ttl-form" onSubmit={save} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <Field label="Minutos">{(id) => <Input id={id} type="number" min={5} value={minutes || current} onChange={(e) => setMinutes(e.target.value)} />}</Field>
      </form>
    </Modal>
  );
}

const STATUS_OPTIONS = ["pending", "executing", "executed", "execution_failed", "rejected", "expired", "cancelled", ""];

export default function ToolsPage() {
  const tools = useApi(() => api.get<Tool[]>("/api/v1/tools"), []);
  const overrides = useApi(() => api.get<OrganizationToolSetting[]>("/api/v1/tools/approval-ttl"), []);
  const [statusFilter, setStatusFilter] = useState("pending");
  const approvals = useApi(() => api.get<Approval[]>(`/api/v1/approvals${statusFilter ? `?status=${statusFilter}` : ""}`), [statusFilter], { pollMs: 7000 });
  const decideAction = useAction();
  const [executing, setExecuting] = useState<Tool | null>(null);
  const [ttlTool, setTtlTool] = useState<Tool | null>(null);
  const [deciding, setDeciding] = useState<{ a: Approval; approve: boolean } | null>(null);
  const [reason, setReason] = useState("");

  async function confirmDecision(e: React.FormEvent) {
    e.preventDefault();
    if (!deciding) return;
    const { a, approve } = deciding;
    if (await decideAction.run(() => api.post(`/api/v1/approvals/${a.id}/decide`, { approve, reason }), approve ? "Pedido aprovado." : "Pedido rejeitado.")) { setDeciding(null); setReason(""); approvals.reload(); }
  }
  async function cancel(a: Approval) {
    if (await decideAction.run(() => api.post(`/api/v1/approvals/${a.id}/cancel`), "Pedido cancelado.")) approvals.reload();
  }

  const toolColumns: Column<ToolRow>[] = [
    { key: "key", header: "Tool", primary: true, cell: (t) => <div><p className="font-mono text-xs font-medium">{t.key}</p><p className="text-xs text-nd-muted">{t.description}</p></div> },
    { key: "risk", header: "Risco", cell: (t) => <Badge tone={RISK[t.risk_level]}>{t.risk_level}</Badge> },
    { key: "perm", header: "Permissão", cell: (t) => <span className="font-mono text-xs text-nd-muted">{t.required_permission}</span> },
    { key: "impl", header: "Estado", cell: (t) => t.implemented ? <Badge tone="green">implementada</Badge> : <Badge>não implementada</Badge> },
    { key: "ttl", header: "Validade", cell: (t) => {
      if (!isGated(t)) return <span className="text-nd-faint">—</span>;
      const o = overrides.data?.find((x) => x.tool_key === t.key);
      return <button type="button" onClick={() => setTtlTool(t)} className="inline-flex items-center gap-1.5 text-xs text-nd-muted hover:text-nd-text"><Clock className="h-3.5 w-3.5" aria-hidden />{o ? formatTTL(o.approval_ttl_seconds) : `${formatTTL(DEFAULT_TTL)} (defeito)`}</button>;
    } },
    { key: "act", header: "", className: "text-right", hideOnMobile: true, cell: (t) => <Button size="sm" icon={<Play className="h-3.5 w-3.5" />} onClick={() => setExecuting(t)}>Executar</Button> },
  ];

  const approvalColumns: Column<Approval>[] = [
    { key: "action", header: "Ação", primary: true, cell: (a) => <span className="font-mono text-xs font-medium">{a.requested_action}</span> },
    { key: "risk", header: "Risco", cell: (a) => <Badge tone={RISK[a.risk_level]}>{a.risk_level}</Badge> },
    { key: "res", header: "Recurso", cell: (a) => <span className="text-xs text-nd-muted">{a.resource_type}:{a.resource_id}</span> },
    { key: "st", header: "Estado", cell: (a) => <StatusBadge status={a.status} /> },
    { key: "by", header: "Pedido por", cell: (a) => <span className="text-xs text-nd-muted">{a.requested_by_agent_id ? "agente" : a.requested_by_user_id ? "utilizador" : "—"}</span> },
    { key: "cr", header: "Pedido", cell: (a) => <span className="text-xs text-nd-faint">{new Date(a.created_at).toLocaleString("pt-PT")}</span> },
    { key: "act", header: "", className: "text-right", hideOnMobile: true, cell: (a) => a.status !== "pending" ? null : (
      <div className="flex justify-end gap-1.5">
        <Button size="sm" icon={<Check className="h-3.5 w-3.5" />} onClick={() => { setDeciding({ a, approve: true }); setReason(""); decideAction.setError(null); }}>Aprovar</Button>
        <Button size="sm" variant="danger" icon={<X className="h-3.5 w-3.5" />} onClick={() => { setDeciding({ a, approve: false }); setReason(""); decideAction.setError(null); }}>Rejeitar</Button>
        <RowMenu label="Mais ações" items={[{ label: "Cancelar pedido", onSelect: () => cancel(a) }]} />
      </div>) },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="Tools & Aprovações" description="O Tool Gateway. Tools de leitura/seguras correm de imediato; as privileged/critical criam um pedido de aprovação." />
      <Section title="Catálogo de tools">
        <DataTable caption="Tools" columns={toolColumns} rows={tools.data ? tools.data.map((t) => ({ ...t, id: t.key })) : null} loading={tools.loading} error={tools.error} onRetry={tools.reload} pageSize={12}
          empty={{ icon: <ShieldCheck />, title: "Sem tools registadas" }} />
      </Section>

      <Section title="Aprovações" description="Atualiza automaticamente a cada 7 s." actions={
        <Select aria-label="Filtrar por estado" className="w-44" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          {STATUS_OPTIONS.map((s) => <option key={s} value={s}>{s || "todos"}</option>)}
        </Select>}>
        {decideAction.error && !deciding && <Alert tone="error" className="mb-3">{decideAction.error}</Alert>}
        <DataTable caption="Aprovações" columns={approvalColumns} rows={approvals.data} loading={approvals.loading && !approvals.data} error={approvals.error} onRetry={approvals.reload}
          empty={{ icon: <ShieldCheck />, title: `Sem aprovações ${statusFilter || ""}`.trim() }} />
      </Section>

      <ExecuteModal tool={executing} onClose={() => setExecuting(null)} onApproval={() => approvals.reload()} />
      <TTLModal key={ttlTool?.key ?? "none"} tool={ttlTool} override={overrides.data?.find((o) => o.tool_key === ttlTool?.key)} onClose={() => setTtlTool(null)} onChanged={() => overrides.reload()} />
      <Modal open={!!deciding} onClose={() => setDeciding(null)} size="sm" title={deciding?.approve ? "Aprovar pedido" : "Rejeitar pedido"}
        description={deciding ? `${deciding.a.requested_action} · ${deciding.a.resource_type}:${deciding.a.resource_id}${deciding.approve ? " — aprovar executa a tool de imediato." : ""}` : undefined}
        footer={<><Button onClick={() => setDeciding(null)}>Cancelar</Button><Button variant={deciding?.approve ? "primary" : "danger"} type="submit" form="decide-form" disabled={decideAction.busy}>{deciding?.approve ? "Aprovar e executar" : "Rejeitar"}</Button></>}>
        <form id="decide-form" onSubmit={confirmDecision} className="space-y-4">
          {decideAction.error && <Alert tone="error">{decideAction.error}</Alert>}
          <Field label="Motivo (opcional)">{(id) => <Input id={id} value={reason} onChange={(e) => setReason(e.target.value)} />}</Field>
        </form>
      </Modal>
    </div>
  );
}
