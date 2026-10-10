"use client";

import { useRef, useState } from "react";
import { Rocket, Upload } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Card, Section } from "@/components/ui/Card";
import { PageHeader } from "@/components/ui/PageHeader";
import { Alert } from "@/components/ui/Feedback";
import { Field, Input, Select } from "@/components/ui/Forms";
import { Stepper } from "@/components/ui/Stepper";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { StatusText, Badge } from "@/components/ui/Status";
import { usePlatform } from "@/components/providers/Platform";
import { useOperations } from "@/components/providers/Operations";
import { toStatus } from "@/lib/status";
import { ago } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { deploymentsService, projectsService } from "@/services";
import type { ApiDeployment } from "@/lib/types";

const STAGES = ["PRECHECK", "FETCH", "BUILD", "TEST", "DEPLOY", "HEALTH_CHECK", "COMPLETE"];

// Reads files client-side as base64; paths come from webkitRelativePath so a folder upload keeps its structure.
async function readFiles(list: FileList): Promise<Record<string, string>> {
  const out: Record<string, string> = {};
  for (const f of Array.from(list)) {
    const buf = new Uint8Array(await f.arrayBuffer());
    let bin = ""; for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000));
    const rel = (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name;
    out[rel.split("/").length > 1 && list.length > 1 ? rel.split("/").slice(1).join("/") : rel] = btoa(bin);
  }
  return out;
}

export default function DeploymentPage() {
  const { can } = usePlatform();
  const ops = useOperations();
  const projects = useAsync(() => projectsService.list({ status: "active" }));
  const deployments = useAsync(() => deploymentsService.list());
  const [f, setF] = useState({ project: "", source: "upload", repository: "", ref: "main", environment: "staging" });
  const [files, setFiles] = useState<Record<string, string>>({});
  const input = useRef<HTMLInputElement>(null);
  const last = deployments.data?.[0];

  async function deploy() {
    const body: Parameters<typeof deploymentsService.create>[1] = { source: f.source, environment: f.environment };
    if (f.source !== "upload") { body.repository = f.repository; body.ref = f.ref; } else { body.files = files; }
    await ops.submit(f.environment === "production" ? "Deployment para produção" : "Deployment", () => deploymentsService.create(f.project, body) as never, deployments.reload);
  }
  const cols: Column<ApiDeployment>[] = [
    { key: "p", header: "Projeto", primary: true, cell: (d) => <span className="font-semibold">{projects.data?.find((p) => p.id === d.project_id)?.name ?? d.project_id.slice(0, 8)}</span> },
    { key: "r", header: "Origem", cell: (d) => <span className="text-nd-muted">{d.source === "upload" ? "upload" : `${d.repository}@${d.ref}`}{d.commit_sha ? ` · ${d.commit_sha.slice(0, 7)}` : ""}</span> },
    { key: "e", header: "Ambiente", cell: (d) => <Badge tone={d.environment === "production" ? "orange" : "neutral"}>{d.environment}</Badge> },
    { key: "s", header: "Estado", cell: (d) => <span title={d.error}><StatusText {...toStatus(d.status)} /></span> },
    { key: "t", header: "Quando", cell: (d) => <span className="text-nd-muted">{ago(d.created_at)}</span> },
    { key: "a", header: "", className: "text-right", hideOnMobile: true, cell: (d) => d.status === "succeeded" && can("deployments.rollback") ? <Button size="sm" onClick={() => ops.submit("Rollback", () => deploymentsService.rollback(d.project_id, d.id), deployments.reload)}>Restaurar esta versão</Button> : null },
  ];
  const stageIdx = last ? Math.max(0, STAGES.indexOf(last.stage)) : 0;
  const ready = f.project && (f.source === "upload" ? Object.keys(files).length > 0 : f.repository.trim());

  return (
    <div className="space-y-6">
      <PageHeader title="Deployment" description="Publica uma nova versão num projeto ativo. Produção exige aprovação; falhas repõem automaticamente a versão anterior." />
      {last && <Card className="p-5"><p className="mb-3 text-xs text-nd-muted">Último deployment: <StatusText {...toStatus(last.status)} /> · fase {last.stage}</p><Stepper steps={STAGES} current={last.status === "failed" ? stageIdx : last.status === "succeeded" ? STAGES.length - 1 : stageIdx} /></Card>}
      <div className="grid gap-6 lg:grid-cols-[minmax(0,420px)_1fr]">
        <Section title="Novo deployment">
          {!can("deployments.create") ? <Alert tone="info">Não tens permissão para criar deployments.</Alert> : (
            <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); void deploy(); }}>
              <Field label="Projeto">{(id) => <Select id={id} value={f.project} onChange={(e) => setF({ ...f, project: e.target.value })} required><option value="">Escolhe…</option>{(projects.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</Select>}</Field>
              <Field label="Origem">{(id) => <Select id={id} value={f.source} onChange={(e) => setF({ ...f, source: e.target.value })}><option value="upload">Upload de ficheiros</option><option value="github">GitHub</option><option value="git">Git</option></Select>}</Field>
              {f.source === "upload" ? (
                <Field label="Ficheiros" hint={Object.keys(files).length ? `${Object.keys(files).length} ficheiro(s) prontos` : "Escolhe os ficheiros do site (máx. 200 MB no total)"}>{() => (
                  <>
                    <input ref={input} type="file" multiple className="sr-only" onChange={async (e) => e.target.files && setFiles(await readFiles(e.target.files))} aria-label="Ficheiros do deployment" />
                    <Button type="button" icon={<Upload className="h-4 w-4" />} onClick={() => input.current?.click()}>Escolher ficheiros</Button>
                  </>)}</Field>
              ) : (<>
                <Field label="Repositório" hint="owner/nome">{(id) => <Input id={id} value={f.repository} onChange={(e) => setF({ ...f, repository: e.target.value })} placeholder="pixart/loja" required />}</Field>
                <Field label="Branch / tag">{(id) => <Input id={id} value={f.ref} onChange={(e) => setF({ ...f, ref: e.target.value })} />}</Field>
              </>)}
              <Field label="Ambiente">{(id) => <Select id={id} value={f.environment} onChange={(e) => setF({ ...f, environment: e.target.value })}><option value="staging">staging</option><option value="production">production (requer aprovação)</option></Select>}</Field>
              {f.environment === "production" && <Alert tone="warning">Vai ser criado um pedido de aprovação. O deployment só corre depois de aprovado.</Alert>}
              <Button type="submit" variant="primary" icon={<Rocket className="h-4 w-4" />} disabled={!ready}>{f.environment === "production" ? "Pedir aprovação" : "Fazer deployment"}</Button>
            </form>)}
        </Section>
        <Section title="Histórico">
          <DataTable caption="Deployments" columns={cols} rows={deployments.data ?? []} loading={deployments.loading} error={deployments.error} onRetry={deployments.reload}
            empty={{ icon: <Rocket />, title: "Ainda sem deployments", description: "O primeiro aparece aqui assim que o fizeres." }} />
        </Section>
      </div>
    </div>
  );
}
