"use client";

import { useRef, useState } from "react";
import { Check, Upload, X, AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Card, Section } from "@/components/ui/Card";
import { PageHeader } from "@/components/ui/PageHeader";
import { Alert } from "@/components/ui/Feedback";
import { Field, Input, Select } from "@/components/ui/Forms";
import { Stepper } from "@/components/ui/Stepper";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { Badge, StatusText } from "@/components/ui/Status";
import { usePlatform } from "@/components/providers/Platform";
import { useOperations } from "@/components/providers/Operations";
import { useToast } from "@/components/ui/Toast";
import { ApiError } from "@/lib/api";
import { toStatus } from "@/lib/status";
import { ago } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { migrationsService, projectsService } from "@/services";
import type { ApiMigration } from "@/lib/types";

const STEPS = ["SOURCE", "PREFLIGHT", "CONFIGURATION", "TRANSFER", "VALIDATION", "CUTOVER", "COMPLETE"];
type Check = { id: string; title: string; status: "PASS" | "WARNING" | "BLOCKER"; detail?: string };

// Which stepper position a migration status corresponds to.
function stepOf(m: ApiMigration | null): number {
  if (!m) return 0;
  switch (m.status) {
    case "discovering": return m.has_source_archive ? 1 : 0;
    case "planned": case "preflight_failed": return 2;
    case "transferring": case "importing": return 3;
    case "validating": return 4;
    case "ready_for_cutover": return 5;
    case "cutting_over": return 5;
    case "completed": case "rolled_back": return 6;
    default: return 3;
  }
}

function checksOf(m: ApiMigration): Check[] {
  const r = (m.preflight_report as { report?: { checks?: Check[] } } | undefined)?.report;
  return r?.checks ?? [];
}
const ICON = { PASS: <Check className="h-4 w-4 text-nd-success" aria-label="OK" />, WARNING: <AlertTriangle className="h-4 w-4 text-nd-warning" aria-label="aviso" />, BLOCKER: <X className="h-4 w-4 text-nd-danger" aria-label="bloqueio" /> };

export default function MigrationPage() {
  const { can } = usePlatform();
  const ops = useOperations();
  const toast = useToast();
  const projects = useAsync(() => projectsService.list({ kind: "wordpress", status: "active" }));
  const list = useAsync(() => migrationsService.list());
  const [mig, setMig] = useState<ApiMigration | null>(null);
  const [f, setF] = useState({ project: "", domain: "", source_url: "", kind: "zip" });
  const [busy, setBusy] = useState(false);
  const file = useRef<HTMLInputElement>(null);
  const create = can("migrations.create"), cutover = can("migrations.cutover");

  const refresh = async (id = mig?.id) => { if (!id) return; setMig(await migrationsService.get(id)); list.reload(); };
  const guard = async (fn: () => Promise<unknown>) => { setBusy(true); try { await fn(); } catch (e) { toast.push("error", e instanceof ApiError ? e.message : e instanceof Error ? e.message : "Falhou"); } finally { setBusy(false); } };

  const step = stepOf(mig);
  const checks = mig ? checksOf(mig) : [];
  const blockers = checks.filter((c) => c.status === "BLOCKER");
  const cols: Column<ApiMigration>[] = [
    { key: "d", header: "Destino", primary: true, cell: (m) => <button type="button" className="font-semibold hover:text-nd-primary-soft" onClick={() => setMig(m)}>{m.target_domain}</button> },
    { key: "k", header: "Origem", cell: (m) => <Badge>{m.source_kind}</Badge> },
    { key: "s", header: "Estado", cell: (m) => <StatusText {...toStatus(m.status)} /> },
    { key: "h", header: "Saúde", hideOnMobile: true, cell: (m) => m.health_score != null ? <span className="tabular-nums">{m.health_score}/100</span> : "—" },
    { key: "t", header: "Atualizado", hideOnMobile: true, cell: (m) => <span className="text-nd-muted">{ago(m.updated_at)}</span> },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="Migração WordPress" description="Planeia, transfere para uma área de staging, valida e só então faz o cutover. O site atual nunca é tocado antes do cutover aprovado." />
      <Card className="p-5"><Stepper steps={STEPS} current={step} /></Card>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,440px)_1fr]">
        <Section title={mig ? `Migração para ${mig.target_domain}` : "Nova migração"}>
          {!mig ? (
            !create ? <Alert tone="info">Não tens permissão para criar migrações.</Alert> : (
            <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); void guard(async () => { const m = await migrationsService.create({ project_id: f.project || null, source_kind: f.kind, target_domain: f.domain, source_url: f.source_url || undefined }); setMig(m); list.reload(); }); }}>
              <Field label="Tipo de origem">{(id) => <Select id={id} value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value })}><option value="zip">Arquivo ZIP (wp-content + database.sql)</option><option value="updraftplus">UpdraftPlus (zip)</option><option value="manual">Manual (zip)</option><option value="ftp" disabled>FTP — conector não disponível</option><option value="sftp" disabled>SFTP — conector não disponível</option><option value="ssh" disabled>SSH — conector não disponível</option><option value="cpanel" disabled>cPanel — conector não disponível</option></Select>}</Field>
              <Field label="Projeto WordPress de destino" hint="Cria e provisiona o projeto primeiro.">{(id) => <Select id={id} value={f.project} onChange={(e) => setF({ ...f, project: e.target.value })} required><option value="">Escolhe…</option>{(projects.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</Select>}</Field>
              <Field label="Domínio de destino">{(id) => <Input id={id} value={f.domain} onChange={(e) => setF({ ...f, domain: e.target.value })} placeholder="novo.exemplo.pt" required />}</Field>
              <Field label="URL atual do site (opcional)" hint="Se vazio, é detetado a partir da base de dados.">{(id) => <Input id={id} value={f.source_url} onChange={(e) => setF({ ...f, source_url: e.target.value })} placeholder="https://antigo.exemplo.pt" />}</Field>
              <Button type="submit" variant="primary" disabled={busy || !f.project || !f.domain}>Criar migração</Button>
            </form>)
          ) : (
            <div className="space-y-4">
              <p className="text-sm text-nd-muted">Estado: <StatusText {...toStatus(mig.status)} />{mig.health_score != null && <> · saúde <b className="text-nd-text">{mig.health_score}/100</b></>}</p>
              {mig.error && <Alert tone="error">{mig.error}</Alert>}

              {["zip", "updraftplus", "manual"].includes(mig.source_kind) && ["discovering", "planned", "preflight_failed"].includes(mig.status) && (
                <div>
                  <input ref={file} type="file" accept=".zip,application/zip" className="sr-only" aria-label="Arquivo ZIP" onChange={(e) => { const fl = e.target.files?.[0]; if (fl) void guard(async () => { setMig(await migrationsService.uploadSource(mig.id, fl)); toast.push("success", "Arquivo carregado."); }); }} />
                  <Button icon={<Upload className="h-4 w-4" />} disabled={busy} onClick={() => file.current?.click()}>{mig.has_source_archive ? "Substituir arquivo" : "Carregar arquivo ZIP"}</Button>
                  <p className="mt-1 text-xs text-nd-faint">Máx. 64 MB. wp-config.php nunca é migrado (contém credenciais do servidor antigo).</p>
                </div>)}

              {["discovering", "planned", "preflight_failed"].includes(mig.status) && <Button disabled={busy || !mig.has_source_archive} onClick={() => ops.submit("Plano e preflight", () => migrationsService.plan(mig.id), () => void refresh())}>{mig.status === "discovering" ? "Analisar (preflight)" : "Repetir análise"}</Button>}
              {mig.status === "planned" && <Button variant="primary" disabled={busy} onClick={() => ops.submit("Transferir para staging", () => migrationsService.run(mig.id), () => void refresh())}>Transferir para staging</Button>}
              {mig.status === "ready_for_cutover" && cutover && <Button variant="primary" disabled={busy} onClick={() => ops.submit("Cutover", async () => (await migrationsService.cutover(mig.id)) as never, () => void refresh())}>Pedir cutover (aprovação)</Button>}
              {mig.status === "completed" && cutover && <Button variant="danger" disabled={busy} onClick={() => ops.submit("Reverter migração", () => migrationsService.rollback(mig.id), () => void refresh())}>Reverter para o site anterior</Button>}
              <div className="flex gap-2"><Button size="sm" onClick={() => void refresh()}>Atualizar estado</Button><Button size="sm" onClick={() => setMig(null)}>Fechar</Button></div>
            </div>
          )}
        </Section>

        <div className="space-y-6">
          {mig && checks.length > 0 && (
            <Section title="Preflight" description={blockers.length ? `${blockers.length} bloqueio(s) — corrige antes de continuar` : "Sem bloqueios"}>
              <ul className="divide-y divide-nd-border/50">{checks.map((c) => <li key={c.id} className="flex items-start gap-3 py-2.5 text-sm">{ICON[c.status]}<div><p className="text-nd-text">{c.title}</p>{c.detail && <p className="text-xs text-nd-muted">{c.detail}</p>}</div></li>)}</ul>
            </Section>)}
          <Section title="Migrações">
            <DataTable caption="Migrações" columns={cols} rows={list.data ?? []} loading={list.loading} error={list.error} onRetry={list.reload} empty={{ title: "Ainda sem migrações", description: "Cria a primeira ao lado." }} />
          </Section>
        </div>
      </div>
    </div>
  );
}
