"use client";

import { Suspense, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Box, Plus } from "lucide-react";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input, Select, SearchInput } from "@/components/ui/Forms";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { FormModal } from "@/components/ui/FormModal";
import { ConfirmDialog } from "@/components/ui/Overlay";
import { ProjectIdentity, ProjectStatus, ProjectTypeBadge } from "@/components/projects/ProjectParts";
import { Dropdown } from "@/components/ui/Dropdown";
import { usePlatform } from "@/components/providers/Platform";
import { useOperations } from "@/components/providers/Operations";
import { useToast } from "@/components/ui/Toast";
import { useAsync } from "@/lib/useAsync";
import { ago } from "@/lib/format";
import { clientsService, projectsService } from "@/services";
import type { Project } from "@/lib/domain";

function ProjectsInner() {
  const router = useRouter();
  const params = useSearchParams();
  const { can } = usePlatform();
  const ops = useOperations();
  const toast = useToast();
  const [q, setQ] = useState(""); const [status, setStatus] = useState(""); const [kind, setKind] = useState(""); const [client, setClient] = useState("");
  const { data, loading, error, reload } = useAsync(() => projectsService.list({ q, status, kind, client_id: client }), [q, status, kind, client]);
  const clients = useAsync(() => clientsService.list());

  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ name: "", kind: "wordpress", client_id: "", image: "", domain: "" });
  const [del, setDel] = useState<Project | null>(null);

  useEffect(() => {
    const n = params.get("new");
    if (n) { setCreating(true); if (n === "wordpress" || n === "application") setForm((f) => ({ ...f, kind: n })); router.replace("/projects"); }
  }, [params, router]);

  const rows = data ?? [];
  const columns: Column<Project>[] = [
    { key: "name", header: "Nome", primary: true, cell: (p) => <ProjectIdentity project={p} /> },
    { key: "type", header: "Tipo", cell: (p) => <ProjectTypeBadge type={p.type} /> },
    { key: "client", header: "Cliente", hideOnMobile: true, cell: (p) => <span className="text-nd-muted">{p.clientName ?? "—"}</span> },
    { key: "status", header: "Estado", cell: (p) => <ProjectStatus status={p.status} label={p.statusLabel} /> },
    { key: "created", header: "Criado", hideOnMobile: true, cell: (p) => <span className="text-nd-muted">{ago(p.createdAt)}</span> },
    { key: "actions", header: "", className: "w-32 text-right", hideOnMobile: true, cell: (p) => (
      <div className="flex items-center justify-end gap-1">
        <Link href={`/projects/${p.id}`}><Button size="sm">Gerir</Button></Link>
        <Dropdown label={`Mais ações para ${p.name}`} trigger={<span className="flex h-8 w-8 items-center justify-center rounded-md text-nd-muted hover:bg-nd-hover">⋮</span>}
          items={[
            ...(p.status === "PENDING" || p.status === "ERROR" ? [{ label: p.status === "ERROR" ? "Tentar provisionar de novo" : "Provisionar", disabled: !can("projects.create"), onSelect: () => ops.submit(`Provisionar ${p.name}`, () => projectsService.provision(p.id), reload) }] : []),
            { label: "Eliminar…", danger: true, separatorBefore: true, disabled: !can("projects.delete"), onSelect: () => setDel(p) },
          ]} />
      </div>) },
  ];

  async function create() {
    const config: Record<string, unknown> = {};
    if (form.kind === "application" && form.image) config.image = form.image;
    if (form.domain) config.primary_domain = form.domain;
    const p = await projectsService.create({ name: form.name, kind: form.kind as "wordpress" | "application", client_id: form.client_id || null, config });
    setForm({ name: "", kind: "wordpress", client_id: "", image: "", domain: "" });
    toast.push("success", `Projeto “${p.name}” criado. Falta provisionar a infraestrutura.`);
    reload();
  }

  const statuses = ["active", "provisioning", "failed", "degraded", "maintenance"];
  return (
    <div>
      <PageHeader title="Projetos" description="Todos os sites e aplicações geridos pela Nodera."
        actions={can("projects.create") ? <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setCreating(true)}>Novo projeto</Button> : undefined} />
      <Card className="mt-5 p-5">
        <div className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-[1fr_repeat(3,170px)]">
          <SearchInput aria-label="Pesquisar projetos" placeholder="Pesquisar por nome ou slug…" value={q} onChange={(e) => setQ(e.target.value)} />
          <Select aria-label="Filtrar por estado" value={status} onChange={(e) => setStatus(e.target.value)}><option value="">Todos os estados</option>{statuses.map((s) => <option key={s} value={s}>{s}</option>)}</Select>
          <Select aria-label="Filtrar por tipo" value={kind} onChange={(e) => setKind(e.target.value)}><option value="">Todos os tipos</option><option value="wordpress">WordPress</option><option value="application">Aplicação</option></Select>
          <Select aria-label="Filtrar por cliente" value={client} onChange={(e) => setClient(e.target.value)}><option value="">Todos os clientes</option>{(clients.data ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</Select>
        </div>
        <DataTable caption="Lista de projetos" columns={columns} rows={rows} loading={loading} error={error} onRetry={reload}
          empty={{ icon: <Box />, title: "Nenhum projeto encontrado", description: "Ajusta os filtros ou cria um novo projeto.", action: can("projects.create") ? { label: "Novo projeto", onClick: () => setCreating(true) } : undefined }} />
      </Card>

      <FormModal open={creating} onClose={() => setCreating(false)} title="Novo projeto" description="Cria o registo do projeto. A infraestrutura é provisionada num passo seguinte, com progresso em direto." submitLabel="Criar projeto" onSubmit={create} disabled={!form.name.trim()}>
        <Field label="Nome">{(id) => <Input id={id} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="Ex.: Loja da Maria" required autoFocus />}</Field>
        <Field label="Tipo">{(id) => <Select id={id} value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}><option value="wordpress">WordPress</option><option value="application">Aplicação (container)</option></Select>}</Field>
        {form.kind === "application" && <Field label="Imagem do container" hint="Obrigatório para aplicações, ex.: nginx:1.27">{(id) => <Input id={id} value={form.image} onChange={(e) => setForm({ ...form, image: e.target.value })} required />}</Field>}
        <Field label="Domínio principal (opcional)" hint="Apenas informativo; o DNS e o SSL configuram-se em Domínios.">{(id) => <Input id={id} value={form.domain} onChange={(e) => setForm({ ...form, domain: e.target.value })} placeholder="exemplo.pt" />}</Field>
        <Field label="Cliente (opcional)">{(id) => <Select id={id} value={form.client_id} onChange={(e) => setForm({ ...form, client_id: e.target.value })}><option value="">Sem cliente</option>{(clients.data ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</Select>}</Field>
      </FormModal>

      <ConfirmDialog open={!!del} onClose={() => setDel(null)} danger confirmLabel="Pedir eliminação" title={`Eliminar “${del?.name}”?`}
        description="A eliminação remove containers, bases de dados e ficheiros e exige aprovação. Nada acontece até alguém aprovar o pedido."
        onConfirm={() => { if (del) void ops.submit(`Eliminar ${del.name}`, async () => (await projectsService.remove(del.id)) as never, reload); }} />
    </div>
  );
}

export default function ProjectsPage() { return <Suspense><ProjectsInner /></Suspense>; }
