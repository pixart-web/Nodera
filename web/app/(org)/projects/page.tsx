"use client";

import { useMemo, useState } from "react";
import { Box, Plus } from "lucide-react";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Select, SearchInput } from "@/components/ui/Forms";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { NotConnected } from "@/components/ui/Feedback";
import { ProjectIdentity, ProjectMenu, ProjectStatus, ProjectTypeBadge } from "@/components/projects/ProjectParts";
import { useToast } from "@/components/ui/Toast";
import { useAsync } from "@/lib/useAsync";
import { clientsService, projectsService } from "@/services";
import type { Project, ResourceStatus } from "@/lib/domain";
import Link from "next/link";

export default function ProjectsPage() {
  const toast = useToast();
  const { data, loading, error, reload } = useAsync(() => projectsService.list());
  const clients = useAsync(() => clientsService.list());
  const [q, setQ] = useState(""); const [status, setStatus] = useState(""); const [type, setType] = useState(""); const [client, setClient] = useState("");

  const rows = useMemo(() => (data ?? []).filter((p) =>
    (!q || `${p.name} ${p.domain}`.toLowerCase().includes(q.toLowerCase())) && (!status || p.status === status) && (!type || p.type === type) && (!client || p.clientId === client)), [data, q, status, type, client]);

  const columns: Column<Project>[] = [
    { key: "name", header: "Nome", primary: true, cell: (p) => <ProjectIdentity project={p} /> },
    { key: "type", header: "Tipo", cell: (p) => <ProjectTypeBadge type={p.type} /> },
    { key: "domain", header: "Domínio", hideOnMobile: true, cell: (p) => <span className="text-nd-muted">{p.domain}</span> },
    { key: "status", header: "Estado", cell: (p) => <ProjectStatus status={p.status} label={p.statusLabel} /> },
    { key: "containers", header: "Containers", cell: (p) => <span className="tabular-nums">{p.containers}</span> },
    { key: "deploy", header: "Último deploy", cell: (p) => <span className="text-nd-muted">{p.lastDeploy ?? "—"}</span> },
    { key: "backup", header: "Último backup", cell: (p) => <span className="text-nd-muted">{p.lastBackup ?? "—"}</span> },
    { key: "actions", header: "", className: "w-28 text-right", hideOnMobile: true, cell: (p) => <div className="flex items-center justify-end gap-1"><Link href={`/projects/${p.id}`}><Button size="sm">Gerir</Button></Link><ProjectMenu project={p} /></div> },
  ];

  const statuses: ResourceStatus[] = ["ONLINE", "PENDING", "WARNING", "OFFLINE", "DEPLOYING", "MIGRATING", "MAINTENANCE"];
  return (
    <div>
      <PageHeader title="Projetos" description="Todos os sites e aplicações geridos pela Nodera."
        actions={<Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => toast.push("info", "Novo projeto: operação ainda não ligada ao backend.")}>Novo projeto</Button>} />
      <NotConnected />
      <Card className="mt-5 p-5">
        <div className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-[1fr_repeat(3,170px)]">
          <SearchInput aria-label="Pesquisar projetos" placeholder="Pesquisar por nome ou domínio…" value={q} onChange={(e) => setQ(e.target.value)} />
          <Select aria-label="Filtrar por estado" value={status} onChange={(e) => setStatus(e.target.value)}><option value="">Todos os estados</option>{statuses.map((s) => <option key={s} value={s}>{s}</option>)}</Select>
          <Select aria-label="Filtrar por tipo" value={type} onChange={(e) => setType(e.target.value)}><option value="">Todos os tipos</option><option value="WORDPRESS">WordPress</option><option value="APPLICATION">Aplicação</option></Select>
          <Select aria-label="Filtrar por cliente" value={client} onChange={(e) => setClient(e.target.value)}><option value="">Todos os clientes</option>{(clients.data ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</Select>
        </div>
        <DataTable caption="Lista de projetos" columns={columns} rows={rows} loading={loading} error={error} onRetry={reload}
          empty={{ icon: <Box />, title: "Nenhum projeto encontrado", description: "Ajusta os filtros ou cria um novo projeto.", action: { label: "Novo projeto" } }} />
      </Card>
    </div>
  );
}
