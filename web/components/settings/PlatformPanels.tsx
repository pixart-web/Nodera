"use client";

import { useState } from "react";
import { Section } from "@/components/ui/Card";
import { Switch, Field, Input, Select } from "@/components/ui/Forms";
import { Badge } from "@/components/ui/Status";
import { Button } from "@/components/ui/Button";
import { Alert, ErrorState, LoadingState } from "@/components/ui/Feedback";
import { FormModal } from "@/components/ui/FormModal";
import { usePlatform } from "@/components/providers/Platform";
import { useToast } from "@/components/ui/Toast";
import { ApiError } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { flagsService, monitoringService, retentionService } from "@/services";

export function FeatureFlagsPanel() {
  const { can } = usePlatform();
  const toast = useToast();
  const flags = useAsync(() => flagsService.list());
  const manage = can("organization.manage");
  return (
    <Section title="Funcionalidades" description="Liga ou desliga motores para esta organização. O valor por defeito vem da plataforma.">
      {flags.loading ? <LoadingState /> : flags.error ? <ErrorState message={flags.error} onRetry={flags.reload} /> : (
        <ul className="divide-y divide-nd-border/50">{flags.data!.map((f) => (
          <li key={f.key} className="flex items-center justify-between gap-4 py-3 text-sm">
            <div><p className="font-mono text-nd-text">{f.key}</p><p className="text-xs text-nd-muted">{f.description}</p>{f.override != null && <Badge tone="blue" className="mt-1">override desta organização</Badge>}</div>
            <div className="flex items-center gap-3">
              {f.override != null && manage && <Button size="sm" onClick={async () => { try { await flagsService.set(f.key, null); flags.reload(); } catch (e) { toast.push("error", e instanceof ApiError ? e.message : "Falhou"); } }}>Repor</Button>}
              <Switch label={f.key} checked={f.enabled} disabled={!manage} onChange={async (v) => { try { await flagsService.set(f.key, v); flags.reload(); } catch (e) { toast.push("error", e instanceof ApiError ? e.message : "Falhou"); } }} />
            </div>
          </li>))}</ul>)}
    </Section>
  );
}

export function RetentionPanel() {
  const { can } = usePlatform();
  const toast = useToast();
  const r = useAsync(() => retentionService.get());
  const [edit, setEdit] = useState<{ resource: string; days: string } | null>(null);
  return (
    <Section title="Retenção de dados" description="Dados mais antigos são apagados automaticamente. O registo de auditoria só é podado se definires uma política (mínimo 90 dias).">
      {r.loading ? <LoadingState /> : r.error ? <ErrorState message={r.error} onRetry={r.reload} /> : (
        <ul className="divide-y divide-nd-border/50">{r.data!.map((p) => (
          <li key={p.resource} className="flex items-center justify-between py-3 text-sm"><span className="font-mono">{p.resource}</span>
            <span className="flex items-center gap-3 text-nd-muted">{p.retention_days === 0 ? "guardar para sempre" : `${p.retention_days} dias`}{p.default && <Badge>por defeito</Badge>}{can("monitoring.manage") && <Button size="sm" onClick={() => setEdit({ resource: p.resource, days: String(p.retention_days || 365) })}>Alterar</Button>}</span></li>))}</ul>)}
      <FormModal open={!!edit} onClose={() => setEdit(null)} title={`Retenção de ${edit?.resource}`} submitLabel="Guardar"
        onSubmit={async () => { await retentionService.set(edit!.resource, Number(edit!.days)); toast.push("success", "Política guardada."); r.reload(); }}>
        <Field label="Dias">{(id) => <Input id={id} inputMode="numeric" value={edit?.days ?? ""} onChange={(e) => setEdit({ ...edit!, days: e.target.value })} required />}</Field>
      </FormModal>
    </Section>
  );
}

export function ChannelsPanel() {
  const { can } = usePlatform();
  const toast = useToast();
  const ch = useAsync(() => monitoringService.channels());
  const [open, setOpen] = useState(false);
  const [f, setF] = useState({ kind: "webhook", name: "", target: "" });
  return (
    <Section title="Canais de notificação" description="As notificações dentro da app são sempre criadas. Webhooks são validados contra SSRF; o email só envia quando houver SMTP configurado."
      actions={can("monitoring.manage") ? <Button size="sm" variant="primary" onClick={() => setOpen(true)}>Novo canal</Button> : undefined}>
      {ch.loading ? <LoadingState /> : ch.error ? <ErrorState message={ch.error} onRetry={ch.reload} /> : (ch.data ?? []).length === 0 ? <p className="text-sm text-nd-muted">Sem canais externos.</p> : (
        <ul className="divide-y divide-nd-border/50">{ch.data!.map((c) => <li key={c.id} className="flex items-center justify-between py-3 text-sm"><span><Badge>{c.kind}</Badge> <span className="ml-2 font-semibold">{c.name}</span> <span className="text-xs text-nd-muted">{c.target}</span></span>
          {can("monitoring.manage") && <Button size="sm" variant="danger" onClick={async () => { try { await monitoringService.deleteChannel(c.id); ch.reload(); } catch (e) { toast.push("error", e instanceof ApiError ? e.message : "Falhou"); } }}>Apagar</Button>}</li>)}</ul>)}
      {f.kind === "email" && <Alert tone="info" className="mt-3">Sem SMTP configurado, as mensagens para canais de email não são enviadas (a falha fica registada no incidente).</Alert>}
      <FormModal open={open} onClose={() => setOpen(false)} title="Novo canal" submitLabel="Criar" disabled={!f.name.trim()}
        onSubmit={async () => { await monitoringService.createChannel(f); setF({ kind: "webhook", name: "", target: "" }); ch.reload(); }}>
        <Field label="Tipo">{(id) => <Select id={id} value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value })}><option value="webhook">Webhook</option><option value="email">Email</option></Select>}</Field>
        <Field label="Nome">{(id) => <Input id={id} value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} required />}</Field>
        <Field label={f.kind === "webhook" ? "URL" : "Email"}>{(id) => <Input id={id} value={f.target} onChange={(e) => setF({ ...f, target: e.target.value })} required />}</Field>
      </FormModal>
    </Section>
  );
}
