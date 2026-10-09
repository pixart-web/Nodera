"use client";

import { use, useState } from "react";
import Link from "next/link";
import { Activity, Database, ExternalLink, FileText, Folder, Globe, Layers, LockKeyhole, Rocket, ScrollText, Settings, LayoutGrid, ArrowLeft } from "lucide-react";
import { Breadcrumb } from "@/components/ui/Breadcrumb";
import { PageHeader } from "@/components/ui/PageHeader";
import { Button } from "@/components/ui/Button";
import { Card, Section } from "@/components/ui/Card";
import { TabPanel, Tabs } from "@/components/ui/Tabs";
import { StatusBadge, StatusText } from "@/components/ui/Status";
import { EmptyState, ErrorState, LoadingState, NotConnected } from "@/components/ui/Feedback";
import { Avatar } from "@/components/ui/Avatar";
import { useToast } from "@/components/ui/Toast";
import { useAsync } from "@/lib/useAsync";
import { projectsService } from "@/services";

const TABS = [
  { id: "overview", label: "Overview", icon: <LayoutGrid className="h-4 w-4" /> },
  { id: "deployments", label: "Deployments", icon: <Rocket className="h-4 w-4" /> },
  { id: "containers", label: "Containers", icon: <Layers className="h-4 w-4" /> },
  { id: "database", label: "Database", icon: <Database className="h-4 w-4" /> },
  { id: "domain", label: "Domain", icon: <Globe className="h-4 w-4" /> },
  { id: "ssl", label: "SSL", icon: <LockKeyhole className="h-4 w-4" /> },
  { id: "backups", label: "Backups", icon: <Database className="h-4 w-4" /> },
  { id: "logs", label: "Logs", icon: <ScrollText className="h-4 w-4" /> },
  { id: "files", label: "Files", icon: <Folder className="h-4 w-4" /> },
  { id: "settings", label: "Settings", icon: <Settings className="h-4 w-4" /> },
];

export default function ProjectDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const toast = useToast();
  const [tab, setTab] = useState("overview");
  const project = useAsync(() => projectsService.get(id), [id]);
  const containers = useAsync(() => projectsService.containers(id), [id]);
  const deployments = useAsync(() => projectsService.deployments(id), [id]);
  const soon = (w: string) => () => toast.push("info", `${w}: operação ainda não ligada ao backend.`);

  if (project.loading) return <LoadingState rows={3} />;
  if (project.error) return <ErrorState message={project.error} onRetry={project.reload} />;
  const p = project.data;
  if (!p) return <EmptyState title="Projeto não encontrado" action={{ label: "Voltar aos projetos" }} description={`Não existe nenhum projeto com o id “${id}”.`} />;

  return (
    <div>
      <Breadcrumb items={[{ label: "Projetos", href: "/projects" }, { label: p.name }]} />
      <PageHeader
        title={<span className="flex items-center gap-3"><Avatar initial={p.initial} color={p.color} size={44} className={p.color === "#FFFFFF" ? "!text-slate-900" : ""} />{p.name}</span>}
        description={p.domain}
        meta={<StatusBadge status={p.status} label={p.statusLabel} />}
        actions={<>
          <a href={`https://${p.domain}`} target="_blank" rel="noreferrer"><Button icon={<ExternalLink className="h-4 w-4" />}>Abrir</Button></a>
          <Button variant="primary" icon={<Rocket className="h-4 w-4" />} onClick={soon("Deploy")}>Deploy</Button>
          <Button icon={<Database className="h-4 w-4" />} onClick={soon("Backup")}>Backup</Button>
          <Button icon={<ScrollText className="h-4 w-4" />} onClick={() => setTab("logs")}>Logs</Button>
          <Button icon={<Settings className="h-4 w-4" />} onClick={() => setTab("settings")}>Settings</Button>
        </>} />
      <NotConnected />
      <div className="mt-5"><Tabs label="Secções do projeto" tabs={TABS} active={tab} onChange={setTab} /></div>

      <TabPanel id="overview" active={tab}>
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
          {[["Estado", <StatusText key="s" status={p.status} label={p.statusLabel} />], ["Containers", p.containers], ["Último deploy", p.lastDeploy ?? "—"], ["Último backup", p.lastBackup ?? "—"]].map(([k, v]) => (
            <Card key={k as string} className="p-4"><p className="text-xs text-nd-muted">{k}</p><div className="mt-1.5 text-xl font-semibold text-nd-text">{v}</div></Card>
          ))}
        </div>
      </TabPanel>

      <TabPanel id="deployments" active={tab}>
        <Section title="Deployments">
          {deployments.loading ? <LoadingState /> : !deployments.data?.length
            ? <EmptyState icon={<Rocket />} title="Nenhum deployment encontrado" action={{ label: "Fazer deployment", onClick: soon("Deploy") }} />
            : <ul className="divide-y divide-nd-border/50">{deployments.data.map((d) => <li key={d.id} className="flex items-center justify-between py-3 text-sm"><span>{d.ref}</span><span className="flex items-center gap-4"><StatusText status={d.status} /><span className="text-xs text-nd-muted">{d.at}</span></span></li>)}</ul>}
        </Section>
      </TabPanel>

      <TabPanel id="containers" active={tab}>
        <Section title="Containers">
          {containers.loading ? <LoadingState /> : containers.error ? <ErrorState message={containers.error} onRetry={containers.reload} />
            : <ul className="divide-y divide-nd-border/50">{containers.data!.map((c) => <li key={c.id} className="flex flex-wrap items-center justify-between gap-2 py-3 text-sm"><span className="font-mono text-nd-text">{c.name}</span><span className="text-xs text-nd-muted">{c.image}</span><StatusText status={c.status} /><span className="text-xs text-nd-muted">{c.uptime}</span></li>)}</ul>}
        </Section>
      </TabPanel>

      {(["database", "domain", "ssl", "backups", "logs", "files", "settings"] as const).map((t) => (
        <TabPanel key={t} id={t} active={tab}>
          <Card><EmptyState icon={<FileText />} title="Disponível em breve"
            description={t === "logs" ? "Os logs do projeto aparecem aqui quando o backend de logs estiver ligado. Vê entretanto a consola global." : "Esta secção ainda não tem backend ligado. Nada é simulado."}
            action={t === "logs" ? { label: "Abrir consola de logs", onClick: () => (window.location.href = "/logs") } : undefined} /></Card>
        </TabPanel>
      ))}
      <div className="mt-6"><Link href="/projects" className="inline-flex items-center gap-1.5 text-sm text-nd-muted hover:text-nd-text"><ArrowLeft className="h-4 w-4" />Voltar aos projetos</Link></div>
    </div>
  );
}
