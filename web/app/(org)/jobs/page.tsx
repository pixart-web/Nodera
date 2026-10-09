"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Ban, Plus, RotateCw, Wrench } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { useAction } from "@/lib/useAction";
import { PageHeader } from "@/components/ui/PageHeader";
import { Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Alert, LoadingState } from "@/components/ui/Feedback";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { Modal } from "@/components/ui/Overlay";
import { Field, Input } from "@/components/ui/Forms";
import { Segmented } from "@/components/ui/Tabs";
import { StatusBadge } from "@/components/StatusBadge";
import type { Job, Page } from "@/lib/types";

const FILTERS = ["todos", "queued", "running", "succeeded", "failed", "cancelled"] as const;
type Filter = (typeof FILTERS)[number];

export default function JobsPage() {
  return <Suspense fallback={<LoadingState />}><JobsPageInner /></Suspense>;
}

function JobsPageInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const param = searchParams.get("status") ?? "";
  const filter: Filter = (FILTERS as readonly string[]).includes(param) ? (param as Filter) : "todos";
  const statusFilter = filter === "todos" ? "" : filter;
  const [limit, setLimit] = useState(50);

  const jobs = useApi(() => api.get<Page<Job>>(`/api/v1/jobs?limit=${limit}${statusFilter ? `&status=${statusFilter}` : ""}`), [statusFilter, limit], { pollMs: 5000 });
  const enqueueAction = useAction();
  const row = useAction();
  const [showForm, setShowForm] = useState(false);
  const [type, setType] = useState(""); const [payload, setPayload] = useState("{}");
  const [jsonError, setJsonError] = useState<string | null>(null);

  async function enqueue(e: React.FormEvent) {
    e.preventDefault();
    setJsonError(null);
    let parsed: unknown;
    try { parsed = JSON.parse(payload); } catch { setJsonError("O payload tem de ser JSON válido"); return; }
    const ok = await enqueueAction.run(() => api.post("/api/v1/jobs", { type, payload: parsed }), "Job colocado em fila.", "Não foi possível colocar o job em fila");
    if (ok) { setType(""); setPayload("{}"); setShowForm(false); jobs.reload(); }
  }
  // A failed cancel/retry just means the list shows the true state on reload.
  async function act(path: string, success: string) {
    await row.run(() => api.post(path), success);
    jobs.reload();
  }

  const columns: Column<Job>[] = [
    { key: "type", header: "Tipo", primary: true, cell: (j) => <span className="font-mono text-xs font-medium">{j.type}</span> },
    { key: "st", header: "Estado", cell: (j) => <StatusBadge status={j.status} /> },
    { key: "att", header: "Tentativas", cell: (j) => <span className="tabular-nums text-nd-muted">{j.attempts}/{j.max_attempts}</span> },
    { key: "cr", header: "Criado", cell: (j) => <span className="text-xs text-nd-faint">{new Date(j.created_at).toLocaleString("pt-PT")}</span> },
    { key: "err", header: "Erro", cell: (j) => <span className="block max-w-xs truncate text-xs text-nd-danger">{j.error ?? ""}</span> },
    { key: "act", header: "", className: "text-right", hideOnMobile: true, cell: (j) => (
      <>
        {j.status === "queued" && <Button size="sm" variant="danger" icon={<Ban className="h-3.5 w-3.5" />} onClick={() => act(`/api/v1/jobs/${j.id}/cancel`, "Job cancelado.")}>Cancelar</Button>}
        {j.status === "failed" && <Button size="sm" icon={<RotateCw className="h-3.5 w-3.5" />} onClick={() => act(`/api/v1/jobs/${j.id}/retry`, "Job recolocado em fila.")}>Repetir</Button>}
      </>) },
  ];

  return (
    <div>
      <PageHeader title="Jobs" description="Fila de jobs em Postgres. Atualiza automaticamente a cada 5 s."
        actions={<Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => { setShowForm(true); enqueueAction.setError(null); setJsonError(null); }}>Novo job</Button>} />
      <Alert tone="info" className="mb-4">Ainda não há handlers para tipos de job concretos — os jobs ficam registados conforme os chamadores os colocam em fila.</Alert>
      {row.error && <Alert tone="error" className="mb-4">{row.error}</Alert>}
      <Section title="Fila" actions={
        <Segmented label="Filtrar por estado" options={FILTERS} value={filter} onChange={(f) => router.replace(f === "todos" ? "/jobs" : `/jobs?status=${f}`)} />}>
        <DataTable caption="Jobs" columns={columns} rows={jobs.data?.items ?? null} loading={jobs.loading && !jobs.data} error={jobs.error} onRetry={jobs.reload} pageSize={10}
          empty={{ icon: <Wrench />, title: statusFilter ? `Nenhum job com estado “${statusFilter}”` : "Nenhum job", description: "Os jobs aparecem aqui quando forem colocados em fila." }} />
        {jobs.data?.has_more && <div className="mt-3"><Button onClick={() => setLimit((n) => n + 50)}>Carregar mais</Button></div>}
      </Section>

      <Modal open={showForm} onClose={() => setShowForm(false)} title="Novo job"
        footer={<><Button onClick={() => setShowForm(false)}>Cancelar</Button><Button variant="primary" type="submit" form="job-form" disabled={enqueueAction.busy}>{enqueueAction.busy ? "A enviar…" : "Colocar em fila"}</Button></>}>
        <form id="job-form" onSubmit={enqueue} className="space-y-4">
          {(enqueueAction.error || jsonError) && <Alert tone="error">{jsonError ?? enqueueAction.error}</Alert>}
          <Field label="Tipo">{(id) => <Input id={id} value={type} onChange={(e) => setType(e.target.value)} placeholder="ex.: backup.create" required />}</Field>
          <Field label="Payload (JSON)">{(id) => <textarea id={id} className="input font-mono" rows={4} value={payload} onChange={(e) => setPayload(e.target.value)} />}</Field>
        </form>
      </Modal>
    </div>
  );
}
