"use client";

import { useState } from "react";
import { Bot } from "lucide-react";
import { Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Badge, StatusText } from "@/components/ui/Status";
import { Alert, EmptyState, ErrorState, LoadingState } from "@/components/ui/Feedback";
import { Field, Input, Select } from "@/components/ui/Forms";
import { FormModal } from "@/components/ui/FormModal";
import { usePlatform } from "@/components/providers/Platform";
import { useOperations } from "@/components/providers/Operations";
import { useToast } from "@/components/ui/Toast";
import { ApiError } from "@/lib/api";
import { toStatus } from "@/lib/status";
import { ago } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { aiPlansService, projectsService } from "@/services";

// The AI only proposes. A person approves the plan and runs each step with
// their own permissions; dangerous steps still need gateway approval.
export function AIPlansPanel() {
  const { can } = usePlatform();
  const ops = useOperations();
  const toast = useToast();
  const plans = useAsync(() => aiPlansService.list());
  const projects = useAsync(() => projectsService.list());
  const [open, setOpen] = useState(false);
  const [f, setF] = useState({ goal: "", project: "" });
  const fail = (e: unknown) => toast.push("error", e instanceof ApiError ? e.message : "Falhou");

  return (
    <Section title="Planos de IA" description="A IA analisa e propõe; nunca executa. Tu aprovas o plano e corres cada passo."
      actions={can("ai.use") ? <Button size="sm" variant="primary" icon={<Bot className="h-4 w-4" />} onClick={() => setOpen(true)}>Pedir plano</Button> : undefined}>
      {plans.loading ? <LoadingState /> : plans.error ? <ErrorState message={plans.error} onRetry={plans.reload} /> : (plans.data ?? []).length === 0
        ? <EmptyState icon={<Bot />} title="Sem planos" description="Descreve um objetivo e a IA propõe passos usando apenas operações reais da plataforma." />
        : (
          <ul className="space-y-4">{plans.data!.map((p) => (
            <li key={p.id} className="rounded-nd border border-nd-border p-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div><p className="font-semibold text-nd-text">{p.goal}</p><p className="text-xs text-nd-muted">{p.summary || "—"} · {ago(p.created_at)} · {p.model}</p></div>
                <Badge tone={p.status === "approved" ? "green" : p.status === "rejected" ? "red" : p.status === "completed" ? "blue" : "orange"}>{p.status}</Badge>
              </div>
              {p.steps.length === 0 ? <p className="mt-3 text-sm text-nd-muted">O modelo não propôs nenhum passo executável.</p> : (
                <ol className="mt-3 space-y-2">{p.steps.map((s) => (
                  <li key={s.index} className="flex flex-wrap items-center justify-between gap-2 text-sm">
                    <span><span className="font-mono">{s.index + 1}. {s.operation}</span>{s.dangerous && <Badge tone="red" className="ml-2">perigoso · requer aprovação</Badge>}<span className="block text-xs text-nd-muted">{s.rationale}</span>{!s.valid && <span className="block text-xs text-nd-danger">Inválido: {s.issue}</span>}</span>
                    <span className="flex items-center gap-2">
                      {s.status !== "pending" && <StatusText {...toStatus(s.status === "submitted" ? "running" : "pending")} label={s.status === "awaiting_approval" ? "a aguardar aprovação" : "submetido"} />}
                      {p.status === "approved" && s.status === "pending" && s.valid && <Button size="sm" onClick={async () => { try { const r = await aiPlansService.runStep(p.id, s.index); const st = r.steps[s.index]; if (st?.job_id) ops.track(st.job_id, s.operation); else toast.push("info", "Pedido enviado para aprovação."); plans.reload(); } catch (e) { fail(e); } }}>Executar passo</Button>}
                    </span>
                  </li>))}</ol>)}
              {p.status === "proposed" && can("approvals.decide") && (
                <div className="mt-3 flex gap-2"><Button size="sm" variant="primary" onClick={async () => { try { await aiPlansService.decide(p.id, true); plans.reload(); } catch (e) { fail(e); } }}>Aprovar plano</Button><Button size="sm" onClick={async () => { try { await aiPlansService.decide(p.id, false); plans.reload(); } catch (e) { fail(e); } }}>Rejeitar</Button></div>)}
            </li>))}</ul>)}
      <FormModal open={open} onClose={() => setOpen(false)} title="Pedir um plano à IA" submitLabel="Pedir plano" disabled={!f.goal.trim()}
        onSubmit={async () => { await aiPlansService.propose(f.goal, f.project || undefined); setF({ goal: "", project: "" }); plans.reload(); }}>
        <Alert tone="info">O modelo recebe apenas o nome, tipo e estado do projeto e a lista de operações que podes executar — nunca segredos nem configuração.</Alert>
        <Field label="Objetivo">{(id) => <Input id={id} value={f.goal} onChange={(e) => setF({ ...f, goal: e.target.value })} placeholder="Ex.: garantir um backup antes de atualizar o WordPress" required />}</Field>
        <Field label="Projeto (opcional)">{(id) => <Select id={id} value={f.project} onChange={(e) => setF({ ...f, project: e.target.value })}><option value="">Nenhum</option>{(projects.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</Select>}</Field>
      </FormModal>
    </Section>
  );
}
