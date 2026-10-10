"use client";

import { useState } from "react";
import Link from "next/link";
import { Boxes, Plus } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Badge, StatusText } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { Dropdown } from "@/components/ui/Dropdown";
import { ListPage } from "@/components/ui/ListPage";
import { Field, Input } from "@/components/ui/Forms";
import { FormModal } from "@/components/ui/FormModal";
import { usePlatform } from "@/components/providers/Platform";
import { useOperations } from "@/components/providers/Operations";
import { useAsync } from "@/lib/useAsync";
import { ago } from "@/lib/format";
import { backupsService, projectsService } from "@/services";
import type { Project } from "@/lib/domain";
import type { ApiWordPressHealth } from "@/lib/types";

export default function WordPressPage() {
  const { can } = usePlatform();
  const ops = useOperations();
  const { data, loading, error, reload } = useAsync(async () => {
    const sites = await projectsService.list({ kind: "wordpress" });
    const health: Record<string, ApiWordPressHealth | null> = {};
    await Promise.all(sites.filter((s) => s.status === "ONLINE").map(async (s) => { health[s.id] = await projectsService.wpHealth(s.id).catch(() => null); }));
    const backups = await backupsService.list().catch(() => []);
    return { sites, health, backups };
  });
  const [clone, setClone] = useState<Project | null>(null);
  const [upd, setUpd] = useState<Project | null>(null);
  const [f, setF] = useState({ name: "", domain: "", tag: "6-php8.3-apache" });

  const lastBackup = (id: string) => data?.backups.find((b) => b.project_id === id && b.status === "completed")?.created_at;
  const cols: Column<Project>[] = [
    { key: "name", header: "Site", primary: true, cell: (s) => <div><Link href={`/projects/${s.id}`} className="font-semibold hover:text-nd-primary-soft">{s.name}</Link><p className="text-xs text-nd-muted">{s.domain}</p></div> },
    { key: "health", header: "Saúde", cell: (s) => { const h = data?.health[s.id]; return h ? <Badge tone={h.healthy ? "green" : "red"}>{h.healthy ? `Saudável · ${h.score}` : `Problemas · ${h.score}`}</Badge> : <span className="text-nd-faint">—</span>; } },
    { key: "backup", header: "Backup", hideOnMobile: true, cell: (s) => <span className="text-nd-muted">{lastBackup(s.id) ? ago(lastBackup(s.id)) : "nunca"}</span> },
    { key: "status", header: "Estado", cell: (s) => <StatusText status={s.status} label={s.statusLabel} /> },
    { key: "act", header: "", hideOnMobile: true, className: "text-right", cell: (s) => (
      <div className="flex items-center justify-end gap-1.5">
        <Link href={`/projects/${s.id}`}><Button size="sm">Gerir</Button></Link>
        <Dropdown label={`Operações em ${s.name}`} trigger={<span className="inline-flex h-8 items-center rounded-nd border border-nd-strong px-3 text-xs font-medium text-nd-text hover:bg-nd-hover">Operações</span>}
          items={[
            { label: "Backup", disabled: !can("backups.manage") || s.status !== "ONLINE", onSelect: () => ops.submit(`Backup de ${s.name}`, () => backupsService.create(s.id, { type: "full" }), reload) },
            { label: "Clonar…", disabled: !can("wordpress.manage") || s.status !== "ONLINE", onSelect: () => { setF({ ...f, name: `${s.name} (cópia)` }); setClone(s); } },
            { label: "Atualizar WordPress…", disabled: !can("wordpress.manage") || s.status !== "ONLINE", onSelect: () => setUpd(s) },
          ]} />
      </div>) },
  ];
  return (
    <ListPage title="Sites WordPress" description="Instalar = criar um projeto WordPress e provisioná-lo. Migrar traz um site existente."
      actions={<><Link href="/migration"><Button>Migrar site</Button></Link>{can("projects.create") && <Link href="/projects?new=wordpress"><Button variant="primary" icon={<Plus className="h-4 w-4" />}>Novo WordPress</Button></Link>}</>}>
      <DataTable caption="Sites WordPress" columns={cols} rows={data?.sites ?? []} loading={loading} error={error} onRetry={reload}
        empty={{ icon: <Boxes />, title: "Nenhum site WordPress", description: "Cria um projeto WordPress ou migra um site existente." }} />
      <FormModal open={!!clone} onClose={() => setClone(null)} title={`Clonar ${clone?.name}`} description="Copia ficheiros e base de dados para um novo projeto. Se indicares um domínio, os URLs são reescritos." submitLabel="Clonar" disabled={!f.name.trim()}
        onSubmit={async () => { await ops.submit("Clonar site", () => projectsService.wpClone(clone!.id, { name: f.name, domain: f.domain || undefined }), reload); }}>
        <Field label="Nome do clone">{(id) => <Input id={id} value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} required />}</Field>
        <Field label="Domínio do clone (opcional)">{(id) => <Input id={id} value={f.domain} onChange={(e) => setF({ ...f, domain: e.target.value })} placeholder="staging.exemplo.pt" />}</Field>
      </FormModal>
      <FormModal open={!!upd} onClose={() => setUpd(null)} title={`Atualizar WordPress de ${upd?.name}`} description="Faz um backup de segurança, troca a imagem do container (mantendo os dados) e verifica. Se falhar, repõe a imagem anterior." submitLabel="Atualizar" disabled={!f.tag.trim()}
        onSubmit={async () => { await ops.submit("Atualizar WordPress", () => projectsService.wpUpdate(upd!.id, f.tag), reload); }}>
        <Field label="Tag da imagem" hint="Ex.: 6.6-php8.3-apache">{(id) => <Input id={id} value={f.tag} onChange={(e) => setF({ ...f, tag: e.target.value })} required />}</Field>
      </FormModal>
    </ListPage>
  );
}
