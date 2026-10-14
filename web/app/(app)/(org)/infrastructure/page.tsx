"use client";

import { useState } from "react";
import { Plus, Power, Server } from "lucide-react";
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
import { NodeAgentsSection } from "@/components/ops/NodeAgentsSection";
import type { Node as NoderaNode, Page } from "@/lib/types";

const STATUSES = ["online", "offline", "degraded", "unknown"];
const ROLES = ["application", "database", "storage", "worker", "ai-inference", "monitoring"];

export default function InfrastructurePage() {
  const [limit, setLimit] = useState(50);
  const nodes = useApi(() => api.get<Page<NoderaNode>>(`/api/v1/infrastructure/nodes?limit=${limit}`), [limit]);
  const create = useAction();
  const row = useAction();
  const [showForm, setShowForm] = useState(false);
  const [hostname, setHostname] = useState(""); const [provider, setProvider] = useState(""); const [role, setRole] = useState("application");
  const [decom, setDecom] = useState<NoderaNode | null>(null);

  async function register(e: React.FormEvent) {
    e.preventDefault();
    const ok = await create.run(() => api.post("/api/v1/infrastructure/nodes", { hostname, provider, role }), "Node registado.", "Não foi possível registar o node");
    if (ok) { setHostname(""); setProvider(""); setShowForm(false); nodes.reload(); }
  }
  async function setStatus(n: NoderaNode, status: string) {
    if (await row.run(() => api.post(`/api/v1/infrastructure/nodes/${n.id}/status`, { status }), `Estado de ${n.hostname}: ${status}.`)) nodes.reload();
  }
  async function decommission() {
    if (!decom) return;
    if (await row.run(() => api.post(`/api/v1/infrastructure/nodes/${decom.id}/decommission`), "Node desativado.")) nodes.reload();
    setDecom(null);
  }

  const columns: Column<NoderaNode>[] = [
    { key: "host", header: "Hostname", primary: true, cell: (n) => <span className="font-mono text-sm font-medium">{n.hostname}</span> },
    { key: "prov", header: "Provider", cell: (n) => n.provider || "—" },
    { key: "role", header: "Role", cell: (n) => <Badge tone="blue">{n.role}</Badge> },
    { key: "env", header: "Ambiente", cell: (n) => <span className="text-nd-muted">{n.environment}</span> },
    { key: "st", header: "Estado", cell: (n) => <StatusBadge status={n.status} /> },
    { key: "cap", header: "Capabilities", cell: (n) => <span className="text-xs text-nd-muted">{n.capabilities.join(", ") || "—"}</span> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (n) => n.status === "decommissioned" ? null : (
      <RowMenu label={`Ações para ${n.hostname}`} items={[
        ...STATUSES.map((s) => ({ label: `Reportar ${s}`, onSelect: () => setStatus(n, s) })),
        { label: "Desativar node", icon: <Power />, danger: true, separatorBefore: true, onSelect: () => setDecom(n) },
      ]} />) },
  ];

  return (
    <div>
      <PageHeader title="Infraestrutura" description="Inventário de nodes independente do provider."
        actions={<Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => { setShowForm(true); create.setError(null); }}>Registar node</Button>} />
      {row.error && <Alert tone="error" className="mb-4">{row.error}</Alert>}
      <Section title="Nodes" description="Inventário real — não há métricas fabricadas; o estado vem do que for reportado.">
        <DataTable caption="Nodes" columns={columns} rows={nodes.data?.items ?? null} loading={nodes.loading} error={nodes.error} onRetry={nodes.reload} pageSize={10}
          empty={{ icon: <Server />, title: "Nenhum node registado", description: "Isto é inventário real, não um placeholder — regista o primeiro node.", action: { label: "Registar node", onClick: () => setShowForm(true) } }} />
        {nodes.data?.has_more && <div className="mt-3"><Button onClick={() => setLimit((n) => n + 50)}>Carregar mais</Button></div>}
      </Section>

      <div className="mt-6"><NodeAgentsSection nodes={(nodes.data?.items ?? []).filter((n) => n.status !== "decommissioned").map((n) => ({ id: n.id, hostname: n.hostname }))} /></div>

      <Modal open={showForm} onClose={() => setShowForm(false)} title="Registar node"
        footer={<><Button onClick={() => setShowForm(false)}>Cancelar</Button><Button variant="primary" type="submit" form="node-form" disabled={create.busy}>{create.busy ? "A registar…" : "Registar"}</Button></>}>
        <form id="node-form" onSubmit={register} className="space-y-4">
          {create.error && <Alert tone="error">{create.error}</Alert>}
          <Field label="Hostname">{(id) => <Input id={id} value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder="nodera-prod-01" required />}</Field>
          <Field label="Provider">{(id) => <Input id={id} value={provider} onChange={(e) => setProvider(e.target.value)} placeholder="hetzner, local, aws…" />}</Field>
          <Field label="Role">{(id) => <Select id={id} value={role} onChange={(e) => setRole(e.target.value)}>{ROLES.map((r) => <option key={r}>{r}</option>)}</Select>}</Field>
        </form>
      </Modal>
      <ConfirmDialog open={!!decom} onClose={() => setDecom(null)} onConfirm={decommission} danger confirmLabel="Desativar"
        title="Desativar node?" description={`“${decom?.hostname}” passa a decommissioned (terminal — a linha mantém-se para histórico).`} />
    </div>
  );
}
