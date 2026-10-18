"use client";

import { useState } from "react";
import { Bot, MessageSquare, Pencil, Play, Plus, Power, Trash2, Wrench } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { useAction } from "@/lib/useAction";
import { PageHeader } from "@/components/ui/PageHeader";
import { Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Alert } from "@/components/ui/Feedback";
import { Badge } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ConfirmDialog, Modal } from "@/components/ui/Overlay";
import { Field, Input, Select } from "@/components/ui/Forms";
import { RowMenu } from "@/components/ui/RowMenu";
import { StatusBadge } from "@/components/StatusBadge";
import type { Agent, ChatResult, ExecuteResult } from "@/lib/types";

const parseList = (raw: string): string[] => raw.split(",").map((s) => s.trim()).filter(Boolean);

function Chips({ items }: { items: string[] }) {
  if (items.length === 0) return <span className="text-nd-faint">—</span>;
  return <div className="flex flex-wrap gap-1">{items.map((i) => <Badge key={i}>{i}</Badge>)}</div>;
}

function AgentFormModal({ agent, open, onClose, onSaved }: { agent: Agent | null; open: boolean; onClose: () => void; onSaved: () => void }) {
  const action = useAction();
  const [f, setF] = useState({ name: "", description: "", instructions: "", profile: "", tools: "", scope: "" });
  const [loaded, setLoaded] = useState<string | null>(null);
  const key = open ? (agent?.id ?? "new") : null;
  if (key !== loaded) {
    setLoaded(key);
    if (open) setF(agent ? { name: agent.name, description: agent.description, instructions: agent.system_instructions, profile: agent.ai_profile_key, tools: agent.allowed_tool_keys.join(", "), scope: agent.permission_scope.join(", ") } : { name: "", description: "", instructions: "", profile: "", tools: "", scope: "" });
  }
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setF((s) => ({ ...s, [k]: e.target.value }));

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const body = { name: f.name, description: f.description, system_instructions: f.instructions, ai_profile_key: f.profile, allowed_tool_keys: parseList(f.tools), permission_scope: parseList(f.scope) };
    const ok = await action.run(() => (agent ? api.put(`/api/v1/agents/${agent.id}`, body) : api.post("/api/v1/agents", body)), agent ? "Agente atualizado." : "Agente criado (começa desativado).");
    if (ok) { onSaved(); onClose(); }
  }
  return (
    <Modal open={open} onClose={onClose} size="lg" title={agent ? `Editar ${agent.name}` : "Novo agente"} description="O permission_scope nunca pode exceder as tuas próprias permissões — a API rejeita qualquer escalada de privilégios."
      footer={<><Button onClick={onClose}>Cancelar</Button><Button variant="primary" type="submit" form="agent-form" disabled={action.busy}>{action.busy ? "A guardar…" : agent ? "Guardar" : "Criar agente"}</Button></>}>
      <form id="agent-form" onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Nome">{(id) => <Input id={id} value={f.name} onChange={set("name")} required />}</Field>
          <Field label="Perfil de IA (key)">{(id) => <Input id={id} className="font-mono" value={f.profile} onChange={set("profile")} placeholder="ops.assistant" required />}</Field>
        </div>
        <Field label="Descrição">{(id) => <Input id={id} value={f.description} onChange={set("description")} />}</Field>
        <Field label="Instruções de sistema">{(id) => <textarea id={id} className="input font-mono" rows={3} value={f.instructions} onChange={set("instructions")} />}</Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Tools permitidas (vírgulas)">{(id) => <Input id={id} className="font-mono" value={f.tools} onChange={set("tools")} placeholder="check_ssl, get_server_metrics" />}</Field>
          <Field label="Permission scope (vírgulas)">{(id) => <Input id={id} className="font-mono" value={f.scope} onChange={set("scope")} placeholder="ai.use, tools.read" />}</Field>
        </div>
      </form>
    </Modal>
  );
}

function RunModal({ agent, onClose }: { agent: Agent | null; onClose: () => void }) {
  const action = useAction();
  const [message, setMessage] = useState(""); const [result, setResult] = useState<ChatResult | null>(null);
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!agent) return;
    setResult(null);
    let res: ChatResult | null = null;
    if (await action.run(async () => { res = await api.post<ChatResult>(`/api/v1/agents/${agent.id}/run`, { message }); }, undefined, "Não foi possível correr o agente") && res) setResult(res);
  }
  function close() { setResult(null); setMessage(""); action.setError(null); onClose(); }
  return (
    <Modal open={!!agent} onClose={close} size="lg" title={`Conversar com ${agent?.name ?? ""}`} description="Chat com âmbito: usa o perfil de IA e o permission_scope do agente."
      footer={<><Button onClick={close}>Fechar</Button><Button variant="primary" type="submit" form="run-form" disabled={action.busy} icon={<Play className="h-4 w-4" />}>{action.busy ? "A correr…" : "Enviar"}</Button></>}>
      <form id="run-form" onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <Field label="Mensagem">{(id) => <textarea id={id} className="input" rows={3} value={message} onChange={(e) => setMessage(e.target.value)} required />}</Field>
        {result && <div className="rounded-nd border border-nd-border bg-nd-elevated p-3"><p className="whitespace-pre-wrap text-sm text-nd-text">{result.content}</p><p className="mt-2 text-xs text-nd-faint">via {result.provider_key} / {result.model}</p></div>}
      </form>
    </Modal>
  );
}

function ExecuteModal({ agent, onClose }: { agent: Agent | null; onClose: () => void }) {
  const action = useAction();
  const [toolKey, setToolKey] = useState(""); const [rt, setRt] = useState(""); const [rid, setRid] = useState(""); const [params, setParams] = useState("{}");
  const [jsonError, setJsonError] = useState<string | null>(null); const [result, setResult] = useState<ExecuteResult | null>(null);
  const tool = toolKey || agent?.allowed_tool_keys[0] || "";
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!agent) return;
    setJsonError(null); setResult(null);
    let parsed: unknown;
    try { parsed = params.trim() ? JSON.parse(params) : {}; } catch { setJsonError("Os parâmetros têm de ser JSON válido"); return; }
    let res: ExecuteResult | null = null;
    if (await action.run(async () => { res = await api.post<ExecuteResult>(`/api/v1/agents/${agent.id}/tools/${tool}/execute`, { resource_type: rt, resource_id: rid, parameters: parsed }); }, undefined, "Não foi possível executar a tool") && res) setResult(res);
  }
  function close() { setResult(null); setJsonError(null); action.setError(null); onClose(); }
  return (
    <Modal open={!!agent} onClose={close} size="lg" title={`Executar tool como ${agent?.name ?? ""}`} description="Executa uma tool permitida sob a identidade e âmbito do agente. Quem chama decide sempre a tool."
      footer={<><Button onClick={close}>Fechar</Button><Button variant="primary" type="submit" form="agent-exec-form" disabled={action.busy || !tool} icon={<Play className="h-4 w-4" />}>{action.busy ? "A executar…" : "Executar"}</Button></>}>
      {agent && agent.allowed_tool_keys.length === 0 ? <Alert tone="warning">Este agente não tem tools permitidas (allowed_tool_keys).</Alert> : (
        <form id="agent-exec-form" onSubmit={submit} className="space-y-4">
          {(action.error || jsonError) && <Alert tone="error">{jsonError ?? action.error}</Alert>}
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Tool">{(id) => <Select id={id} value={tool} onChange={(e) => setToolKey(e.target.value)}>{agent?.allowed_tool_keys.map((k) => <option key={k}>{k}</option>)}</Select>}</Field>
            <Field label="resource_type">{(id) => <Input id={id} value={rt} onChange={(e) => setRt(e.target.value)} />}</Field>
            <Field label="resource_id">{(id) => <Input id={id} className="font-mono" value={rid} onChange={(e) => setRid(e.target.value)} />}</Field>
          </div>
          <Field label="Parâmetros (JSON)">{(id) => <textarea id={id} className="input font-mono" rows={3} value={params} onChange={(e) => setParams(e.target.value)} />}</Field>
          {result && (result.status === "executed"
            ? <pre className="nd-scroll max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-nd bg-[#050C17] p-3 font-mono text-xs text-nd-text">{JSON.stringify(result.result, null, 2)}</pre>
            : <Alert tone="warning" title="Aprovação necessária">Pedido {result.approval_id} — decide-o em Tools & Aprovações.</Alert>)}
        </form>)}
    </Modal>
  );
}

export default function AgentsPage() {
  const agents = useApi(() => api.get<Agent[]>("/api/v1/agents"), []);
  const row = useAction();
  const [form, setForm] = useState<{ agent: Agent | null } | null>(null);
  const [running, setRunning] = useState<Agent | null>(null);
  const [executing, setExecuting] = useState<Agent | null>(null);
  const [deleting, setDeleting] = useState<Agent | null>(null);

  async function toggle(a: Agent) {
    const path = a.status === "active" ? "disable" : "enable";
    if (await row.run(() => api.post(`/api/v1/agents/${a.id}/${path}`), `Agente ${path === "enable" ? "ativado" : "desativado"}.`)) agents.reload();
  }
  async function remove() {
    if (!deleting) return;
    if (await row.run(() => api.del(`/api/v1/agents/${deleting.id}`), "Agente eliminado.")) agents.reload();
    setDeleting(null);
  }

  const columns: Column<Agent>[] = [
    { key: "name", header: "Agente", primary: true, cell: (a) => <div><p className="text-sm font-semibold">{a.name}</p><p className="font-mono text-xs text-nd-muted">{a.ai_profile_key}</p></div> },
    { key: "tools", header: "Tools permitidas", cell: (a) => <Chips items={a.allowed_tool_keys} /> },
    { key: "scope", header: "Permission scope", cell: (a) => <Chips items={a.permission_scope} /> },
    { key: "st", header: "Estado", cell: (a) => <StatusBadge status={a.status} /> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (a) => (
      <RowMenu label={`Ações para ${a.name}`} items={[
        { label: "Conversar", icon: <MessageSquare />, disabled: a.status !== "active", onSelect: () => setRunning(a) },
        { label: "Executar tool", icon: <Wrench />, disabled: a.status !== "active", onSelect: () => setExecuting(a) },
        { label: "Editar", icon: <Pencil />, separatorBefore: true, onSelect: () => setForm({ agent: a }) },
        { label: a.status === "active" ? "Desativar" : "Ativar", icon: <Power />, onSelect: () => toggle(a) },
        { label: a.status === "disabled" ? "Eliminar" : "Eliminar (desativa primeiro)", icon: <Trash2 />, danger: true, disabled: a.status !== "disabled", onSelect: () => setDeleting(a) },
      ]} />) },
  ];

  return (
    <div>
      <PageHeader title="Agentes" description="Identidade de agente com execução limitada — não é um ciclo autónomo. Quem chama decide sempre que tool o agente usa."
        actions={<Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setForm({ agent: null })}>Novo agente</Button>} />
      {row.error && <Alert tone="error" className="mb-4">{row.error}</Alert>}
      <Section title="Definições de agentes">
        <DataTable caption="Agentes" columns={columns} rows={agents.data} loading={agents.loading} error={agents.error} onRetry={agents.reload}
          empty={{ icon: <Bot />, title: "Ainda não há agentes", description: "Cria um agente com tools e permissões limitadas.", action: { label: "Novo agente", onClick: () => setForm({ agent: null }) } }} />
      </Section>
      <AgentFormModal open={!!form} agent={form?.agent ?? null} onClose={() => setForm(null)} onSaved={() => agents.reload()} />
      <RunModal agent={running} onClose={() => setRunning(null)} />
      <ExecuteModal key={executing?.id ?? "none"} agent={executing} onClose={() => setExecuting(null)} />
      <ConfirmDialog open={!!deleting} onClose={() => setDeleting(null)} onConfirm={remove} danger confirmLabel="Eliminar" title="Eliminar agente?" description={`“${deleting?.name}” será eliminado definitivamente.`} />
    </div>
  );
}
