"use client";

import { useState } from "react";
import { FileText, X } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/ui/PageHeader";
import { Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Badge } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { Field, Input } from "@/components/ui/Forms";
import type { AuditRecord, Page } from "@/lib/types";

export default function AuditPage() {
  const [limit, setLimit] = useState(50);
  const [fromInput, setFromInput] = useState(""); const [toInput, setToInput] = useState("");

  // datetime-local values have no timezone; interpret them in the browser's
  // zone and convert to the RFC3339 timestamp the API's ?from=/?to= expect.
  const from = fromInput ? new Date(fromInput).toISOString() : "";
  const to = toInput ? new Date(toInput).toISOString() : "";
  const audit = useApi(() => api.get<Page<AuditRecord>>(`/api/v1/audit?limit=${limit}${from ? `&from=${encodeURIComponent(from)}` : ""}${to ? `&to=${encodeURIComponent(to)}` : ""}`), [limit, from, to]);

  const columns: Column<AuditRecord>[] = [
    { key: "action", header: "Ação", primary: true, cell: (a) => <span className="font-mono text-xs font-medium">{a.action}</span> },
    { key: "res", header: "Recurso", cell: (a) => <span className="text-xs text-nd-muted">{a.resource_type}{a.resource_id ? `:${a.resource_id.slice(0, 8)}` : ""}</span> },
    { key: "actor", header: "Autor", cell: (a) => <span className="text-xs text-nd-muted">{a.actor_label}</span> },
    { key: "src", header: "Origem", cell: (a) => <Badge>{a.source}</Badge> },
    { key: "ok", header: "Resultado", cell: (a) => <Badge tone={a.success ? "green" : "red"}>{a.success ? "sucesso" : "falhou"}</Badge> },
    { key: "when", header: "Quando", cell: (a) => <span className="text-xs text-nd-faint">{new Date(a.created_at).toLocaleString("pt-PT")}</span> },
  ];

  return (
    <div>
      <PageHeader title="Auditoria" description="Registo append-only. Cada operação sensível fica registada aqui." />
      <Section title="Eventos">
        <div className="mb-4 flex flex-wrap items-end gap-3">
          <Field label="De">{(id) => <Input id={id} type="datetime-local" className="w-56" value={fromInput} onChange={(e) => { setFromInput(e.target.value); setLimit(50); }} />}</Field>
          <Field label="Até">{(id) => <Input id={id} type="datetime-local" className="w-56" value={toInput} onChange={(e) => { setToInput(e.target.value); setLimit(50); }} />}</Field>
          {(fromInput || toInput) && <Button variant="ghost" icon={<X className="h-4 w-4" />} onClick={() => { setFromInput(""); setToInput(""); setLimit(50); }}>Limpar</Button>}
        </div>
        <DataTable caption="Eventos de auditoria" columns={columns} rows={audit.data?.items ?? null} loading={audit.loading && !audit.data} error={audit.error} onRetry={audit.reload} pageSize={15}
          empty={{ icon: <FileText />, title: "Sem registos de auditoria", description: fromInput || toInput ? "Nenhum evento neste intervalo." : "Os eventos aparecem aqui assim que houver atividade." }} />
        {audit.data?.has_more && <div className="mt-3"><Button onClick={() => setLimit((n) => n + 50)}>Carregar mais</Button></div>}
      </Section>
    </div>
  );
}
