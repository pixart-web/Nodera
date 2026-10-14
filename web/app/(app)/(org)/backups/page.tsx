"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Database } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Badge, StatusText } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ListPage } from "@/components/ui/ListPage";
import { Field, Input, Select } from "@/components/ui/Forms";
import { FormModal } from "@/components/ui/FormModal";
import { ConfirmDialog } from "@/components/ui/Overlay";
import { Dropdown } from "@/components/ui/Dropdown";
import { usePlatform } from "@/components/providers/Platform";
import { useOperations } from "@/components/providers/Operations";
import { toStatus } from "@/lib/status";
import { ago, bytes, date } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { backupsService, projectsService } from "@/services";
import type { ApiBackup } from "@/lib/types";

const TYPE_LABEL: Record<string, string> = { full: "Completo", database: "Base de dados", files: "Ficheiros", media: "Media", configuration: "Configuração" };

function BackupsInner() {
  const router = useRouter();
  const params = useSearchParams();
  const { can } = usePlatform();
  const ops = useOperations();
  const { data, loading, error, reload } = useAsync(() => backupsService.list());
  const projects = useAsync(() => projectsService.list());
  const [creating, setCreating] = useState(false);
  const [f, setF] = useState({ project: "", type: "full", days: "30" });
  const [restore, setRestore] = useState<ApiBackup | null>(null);
  const [del, setDel] = useState<ApiBackup | null>(null);
  useEffect(() => { if (params.get("new")) { setCreating(true); router.replace("/backups"); } }, [params, router]);

  const pname = (id: string) => projects.data?.find((p) => p.id === id)?.name ?? id.slice(0, 8);
  const cols: Column<ApiBackup>[] = [
    { key: "p", header: "Projeto", primary: true, cell: (b) => <span className="font-semibold">{pname(b.project_id)}</span> },
    { key: "t", header: "Tipo", cell: (b) => <Badge>{TYPE_LABEL[b.type] ?? b.type}</Badge> },
    { key: "s", header: "Tamanho", cell: (b) => bytes(b.size_bytes) },
    { key: "c", header: "Criado", cell: (b) => <span className="text-nd-muted">{ago(b.created_at)}</span> },
    { key: "st", header: "Estado", cell: (b) => <span title={b.error}><StatusText {...toStatus(b.status)} /></span> },
    { key: "v", header: "Verificado", hideOnMobile: true, cell: (b) => <span className="text-nd-muted">{b.verified_at ? ago(b.verified_at) : "—"}</span> },
    { key: "r", header: "Retenção até", hideOnMobile: true, cell: (b) => <span className="text-nd-muted">{date(b.retention_until)}</span> },
    { key: "a", header: "", hideOnMobile: true, className: "text-right", cell: (b) => (
      <Dropdown label="Ações do backup" trigger={<span className="inline-flex h-8 items-center rounded-nd border border-nd-strong px-3 text-xs font-medium hover:bg-nd-hover">Ações</span>}
        items={[
          { label: "Verificar integridade", disabled: !can("backups.manage"), onSelect: () => ops.submit("Verificar backup", () => backupsService.verify(b.id), reload) },
          { label: "Restaurar…", disabled: !can("backups.restore") || b.status !== "completed", onSelect: () => setRestore(b) },
          { label: "Eliminar…", danger: true, separatorBefore: true, disabled: !can("backups.delete"), onSelect: () => setDel(b) },
        ]} />) },
  ];
  const active = (projects.data ?? []).filter((p) => p.status === "ONLINE");
  return (
    <ListPage title="Backups" description="Cada backup é verificado por checksum. Restaurar exige aprovação e tira antes um snapshot de segurança."
      actions={can("backups.manage") ? <Button variant="primary" onClick={() => setCreating(true)}>Criar backup</Button> : undefined}>
      <DataTable caption="Backups" columns={cols} rows={data ?? []} loading={loading} error={error} onRetry={reload}
        empty={{ icon: <Database />, title: "Nenhum backup encontrado.", description: "Cria um backup manual ou define uma política em Projeto → Backups.", action: can("backups.manage") ? { label: "Criar backup", onClick: () => setCreating(true) } : undefined }} />
      <FormModal open={creating} onClose={() => setCreating(false)} title="Criar backup" description="O backup é criado, verificado (SHA-256) e só então marcado como completo." submitLabel="Criar" disabled={!f.project}
        onSubmit={async () => { await ops.submit("Criar backup", () => backupsService.create(f.project, { type: f.type, retention_days: Number(f.days) || 30 }), reload); }}>
        <Field label="Projeto">{(id) => <Select id={id} value={f.project} onChange={(e) => setF({ ...f, project: e.target.value })} required><option value="">Escolhe…</option>{active.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</Select>}</Field>
        <Field label="Tipo">{(id) => <Select id={id} value={f.type} onChange={(e) => setF({ ...f, type: e.target.value })}>{Object.entries(TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</Select>}</Field>
        <Field label="Retenção (dias)">{(id) => <Input id={id} inputMode="numeric" value={f.days} onChange={(e) => setF({ ...f, days: e.target.value })} />}</Field>
      </FormModal>
      <ConfirmDialog open={!!restore} onClose={() => setRestore(null)} danger confirmLabel="Pedir restauro" title="Restaurar este backup?"
        description="Substitui os dados atuais do projeto. Antes disso é tirado um snapshot de segurança e, se o restauro falhar, é reposto. Requer aprovação."
        onConfirm={() => { if (restore) void ops.submit("Restaurar backup", async () => (await backupsService.restore(restore.id)) as never, reload); }} />
      <ConfirmDialog open={!!del} onClose={() => setDel(null)} danger confirmLabel="Pedir eliminação" title="Eliminar este backup?" description="A eliminação é permanente e requer aprovação."
        onConfirm={() => { if (del) void ops.submit("Eliminar backup", async () => (await backupsService.remove(del.id)) as never, reload); }} />
    </ListPage>
  );
}
export default function BackupsPage() { return <Suspense><BackupsInner /></Suspense>; }
