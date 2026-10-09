"use client";

import { useState } from "react";
import { AppWindow, Plus, Power } from "lucide-react";
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
import type { Application, Page } from "@/lib/types";

const STATUSES = ["running", "stopped", "degraded", "failed", "unknown"];

export default function ApplicationsPage() {
  const [limit, setLimit] = useState(50);
  const apps = useApi(() => api.get<Page<Application>>(`/api/v1/applications?limit=${limit}`), [limit]);
  const create = useAction();
  const row = useAction();
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState(""); const [kind, setKind] = useState("service");
  const [deregistering, setDeregistering] = useState<Application | null>(null);

  async function register(e: React.FormEvent) {
    e.preventDefault();
    const ok = await create.run(() => api.post("/api/v1/applications", { name, kind }), "Aplicação registada.", "Não foi possível registar a aplicação");
    if (ok) { setName(""); setShowForm(false); apps.reload(); }
  }
  async function setStatus(a: Application, status: string) {
    if (await row.run(() => api.post(`/api/v1/applications/${a.id}/status`, { status }), `Estado de ${a.name}: ${status}.`)) apps.reload();
  }
  async function deregister() {
    if (!deregistering) return;
    if (await row.run(() => api.post(`/api/v1/applications/${deregistering.id}/deregister`), "Aplicação removida do registo.")) apps.reload();
    setDeregistering(null);
  }

  const columns: Column<Application>[] = [
    { key: "name", header: "Nome", primary: true, cell: (a) => <span className="font-mono text-sm font-medium">{a.name}</span> },
    { key: "kind", header: "Tipo", cell: (a) => <Badge>{a.kind}</Badge> },
    { key: "env", header: "Ambiente", cell: (a) => <span className="text-nd-muted">{a.environment}</span> },
    { key: "st", header: "Estado", cell: (a) => <StatusBadge status={a.status} /> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (a) => a.status === "deregistered" ? null : (
      <RowMenu label={`Ações para ${a.name}`} items={[
        ...STATUSES.map((s) => ({ label: `Marcar como ${s}`, onSelect: () => setStatus(a, s) })),
        { label: "Remover do registo", icon: <Power />, danger: true, separatorBefore: true, onSelect: () => setDeregistering(a) },
      ]} />) },
  ];

  return (
    <div>
      <PageHeader title="Aplicações" description="Aplicações e serviços registados na organização."
        actions={<Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => { setShowForm(true); create.setError(null); }}>Registar aplicação</Button>} />
      {row.error && <Alert tone="error" className="mb-4">{row.error}</Alert>}
      <Section title="Registo de aplicações" description="Inventário real — o estado é reportado, não inferido.">
        <DataTable caption="Aplicações" columns={columns} rows={apps.data?.items ?? null} loading={apps.loading} error={apps.error} onRetry={apps.reload} pageSize={10}
          empty={{ icon: <AppWindow />, title: "Nenhuma aplicação registada", description: "Regista a primeira aplicação para a acompanhares aqui.", action: { label: "Registar aplicação", onClick: () => setShowForm(true) } }} />
        {apps.data?.has_more && <div className="mt-3"><Button onClick={() => setLimit((n) => n + 50)}>Carregar mais</Button></div>}
      </Section>

      <Modal open={showForm} onClose={() => setShowForm(false)} title="Registar aplicação"
        footer={<><Button onClick={() => setShowForm(false)}>Cancelar</Button><Button variant="primary" type="submit" form="app-form" disabled={create.busy}>{create.busy ? "A registar…" : "Registar"}</Button></>}>
        <form id="app-form" onSubmit={register} className="space-y-4">
          {create.error && <Alert tone="error">{create.error}</Alert>}
          <Field label="Nome">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} required />}</Field>
          <Field label="Tipo">{(id) => <Select id={id} value={kind} onChange={(e) => setKind(e.target.value)}>{["service", "web", "worker", "job", "database"].map((k) => <option key={k}>{k}</option>)}</Select>}</Field>
        </form>
      </Modal>
      <ConfirmDialog open={!!deregistering} onClose={() => setDeregistering(null)} onConfirm={deregister} danger confirmLabel="Remover"
        title="Remover do registo?" description={`“${deregistering?.name}” fica como deregistered (a linha mantém-se para histórico).`} />
    </div>
  );
}
