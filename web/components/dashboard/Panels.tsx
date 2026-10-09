"use client";

import { useState } from "react";
import Link from "next/link";
import { Boxes, CloudUpload, Code2, Database, GitBranch, Globe, Box, Layers, Lock, Plus, RefreshCw, Server, HardDrive, Cpu, MemoryStick, Network, ShieldCheck } from "lucide-react";
import { Card, IconTile, Section, StatCard, type Tone } from "@/components/ui/Card";
import { StatusText } from "@/components/ui/Status";
import { Sparkline, ProgressBar } from "@/components/ui/Charts";
import { Segmented } from "@/components/ui/Tabs";
import { Timeline } from "@/components/ui/Timeline";
import { EmptyState, ErrorState, LoadingState, Skeleton } from "@/components/ui/Feedback";
import { useToast } from "@/components/ui/Toast";
import { ProjectRow } from "@/components/projects/ProjectParts";
import { useAsync } from "@/lib/useAsync";
import { activityService, metricsService, projectsService, servicesHealthService } from "@/services";
import type { Activity, MetricRange, Project, ServiceHealth } from "@/lib/domain";

export interface Totals { projects: number; wordpress: number; databases: number; domains: number; certificates: number }

export function MetricCards({ totals }: { totals: Totals | null }) {
  const cards: Array<{ icon: React.ReactNode; tone: Tone; key: keyof Totals; label: string; badge: string; badgeTone: Tone; href: string }> = [
    { icon: <Box />, tone: "blue", key: "projects", label: "Projetos ativos", badge: "+2", badgeTone: "green", href: "/projects" },
    { icon: <Boxes />, tone: "cyan", key: "wordpress", label: "Sites WordPress", badge: "+1", badgeTone: "blue", href: "/wordpress" },
    { icon: <Database />, tone: "purple", key: "databases", label: "Bases de dados", badge: "+2", badgeTone: "purple", href: "/monitoring" },
    { icon: <Globe />, tone: "orange", key: "domains", label: "Domínios", badge: "+3", badgeTone: "orange", href: "/domains" },
    { icon: <ShieldCheck />, tone: "green", key: "certificates", label: "Certificados SSL", badge: "Todos válidos", badgeTone: "green", href: "/ssl" },
  ];
  return (
    <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-5">
      {cards.map((c) => totals
        ? <StatCard key={c.key} icon={c.icon} tone={c.tone} value={totals[c.key]} label={c.label} badge={c.badge} badgeTone={c.badgeTone} href={c.href} />
        : <Skeleton key={c.key} className="h-[148px] rounded-nd-lg" />)}
    </div>
  );
}

export function QuickActions() {
  const toast = useToast();
  const actions: Array<{ icon: React.ReactNode; tone: Tone; title: string; desc: string; href?: string }> = [
    { icon: <Boxes />, tone: "blue", title: "Novo WordPress", desc: "Instalar site" },
    { icon: <CloudUpload />, tone: "cyan", title: "Migrar WordPress", desc: "Do cPanel / Backup", href: "/migration" },
    { icon: <Code2 />, tone: "blue", title: "Nova Aplicação", desc: "Deploy de projeto", href: "/deployment" },
    { icon: <Globe />, tone: "green", title: "Adicionar Domínio", desc: "Configurar no Traefik", href: "/domains" },
    { icon: <ShieldCheck />, tone: "green", title: "Emitir SSL", desc: "Let's Encrypt", href: "/ssl" },
    { icon: <Database />, tone: "purple", title: "Criar Backup", desc: "Site ou base de dados", href: "/backups" },
  ];
  const cls = "group flex h-full items-center gap-3 rounded-nd-lg border border-nd-border bg-nd-elevated p-3 text-left transition-all duration-200 hover:-translate-y-px hover:border-nd-strong hover:bg-nd-hover";
  const inner = (a: (typeof actions)[number]) => (<><IconTile icon={a.icon} tone={a.tone} /><span className="min-w-0"><span className="block text-sm font-semibold leading-tight text-nd-text">{a.title}</span><span className="block text-xs text-nd-muted">{a.desc}</span></span></>);
  return (
    <Section title="Ações Rápidas">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {actions.map((a) => a.href
          ? <Link key={a.title} href={a.href} className={cls}>{inner(a)}</Link>
          : <button key={a.title} type="button" className={cls} onClick={() => toast.push("info", `${a.title}: operação ainda não ligada ao backend.`)}>{inner(a)}</button>)}
      </div>
    </Section>
  );
}

export function RecentProjects() {
  const { data, loading, error, reload } = useAsync<Project[]>(() => projectsService.list());
  return (
    <Section title="Projetos Recentes" href="/projects">
      {loading ? <LoadingState rows={4} />
        : error ? <ErrorState message={error} onRetry={reload} />
        : !data || data.length === 0 ? <EmptyState icon={<Box />} title="Nenhum projeto ainda" description="Cria o teu primeiro projeto para começar." action={{ label: "Novo projeto" }} />
        : <div className="-mx-2 divide-y divide-nd-border/50">{data.slice(0, 6).map((p) => <ProjectRow key={p.id} project={p} />)}</div>}
    </Section>
  );
}

const ACTIVITY_ICON: Record<Activity["kind"], React.ReactNode> = {
  wordpress: <Boxes />, container: <Layers />, ssl: <Lock />, backup: <Database />, git: <GitBranch />, app: <RefreshCw />,
};

export function ActivityPanel() {
  const { data, loading, error, reload } = useAsync<Activity[]>(() => activityService.list());
  return (
    <Section title="Atividade Recente" href="/audit" linkLabel="Ver todas">
      {loading ? <LoadingState rows={3} />
        : error ? <ErrorState message={error} onRetry={reload} />
        : !data?.length ? <EmptyState title="Sem atividade recente" />
        : <Timeline items={data.map((a) => ({ id: a.id, icon: ACTIVITY_ICON[a.kind], status: a.status, title: a.subject, description: a.event, time: a.at }))} />}
    </Section>
  );
}

export function ServicesStatus() {
  const { data, loading, error, reload } = useAsync<ServiceHealth[]>(() => servicesHealthService.list());
  const allOk = data?.every((s) => s.status === "ONLINE");
  const icons: Record<string, React.ReactNode> = { traefik: <Network />, docker: <Layers />, mariadb: <Database />, redis: <Server />, system: <Cpu /> };
  return (
    <Section title="Status dos Serviços" href="/monitoring" linkLabel="Ver detalhes">
      {loading ? <LoadingState rows={3} /> : error ? <ErrorState message={error} onRetry={reload} /> : (
        <>
          <p className={`-mt-2 mb-3 flex items-center gap-2 text-xs ${allOk ? "text-nd-success" : "text-nd-warning"}`}><span className={`h-2 w-2 rounded-full ${allOk ? "bg-nd-success" : "bg-nd-warning"}`} aria-hidden />{allOk ? "Todos os serviços operacionais" : "Existem serviços com problemas"}</p>
          <ul className="divide-y divide-nd-border/50">
            {data!.map((s) => (
              <li key={s.id} className="grid grid-cols-[1fr_auto_44px] items-center gap-3 py-2.5 text-sm">
                <span className="flex items-center gap-2.5 text-nd-text"><span className="text-nd-success [&>svg]:h-4 [&>svg]:w-4" aria-hidden>{icons[s.id]}</span>{s.name}</span>
                <StatusText status={s.status} />
                <span className="text-right text-xs tabular-nums text-nd-muted">{s.latencyMs}ms</span>
              </li>
            ))}
          </ul>
        </>
      )}
    </Section>
  );
}

const RANGES = ["1h", "6h", "24h", "7d"] as const;

export function ServerUsage() {
  const [range, setRange] = useState<MetricRange>("24h");
  const { data, loading, error, reload } = useAsync(() => metricsService.get(range), [range]);
  const s = (id: string) => data?.series.find((x) => x.id === id)?.points ?? [];
  const items = data ? [
    { id: "cpu", icon: <Cpu />, label: "CPU", value: <>{data.cpu.pct}<span className="text-base text-nd-muted">%</span></>, color: "rgb(var(--color-success))", pts: s("cpu") },
    { id: "ram", icon: <MemoryStick />, label: "Memória RAM", value: <>{data.ram.usedGb} <span className="text-sm text-nd-muted">GB / {data.ram.totalGb} GB</span></>, color: "rgb(var(--color-primary))", pts: s("ram") },
    { id: "disk", icon: <HardDrive />, label: "Disco", value: <>{data.disk.usedGb} <span className="text-sm text-nd-muted">GB / {data.disk.totalGb} GB</span></>, color: "rgb(var(--color-secondary))", pts: s("disk") },
    { id: "net", icon: <Network />, label: "Rede", value: <span className="text-sm"><span className="text-nd-info">↑</span> {data.network.upMbps} Mbps<br /><span className="text-nd-primary-soft">↓</span> {data.network.downMbps} Mbps</span>, color: "rgb(var(--color-warning))", pts: s("network") },
  ] : [];
  return (
    <Section title="Utilização do Servidor" actions={<Segmented label="Período" options={RANGES} value={range} onChange={setRange} />}>
      {loading ? <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-[140px]" />)}</div>
        : error ? <ErrorState message={error} onRetry={reload} />
        : (
          <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
            {items.map((m) => (
              <Card key={m.id} className="overflow-hidden bg-nd-elevated p-4">
                <div className="flex items-center gap-2 text-xs text-nd-muted"><span className="[&>svg]:h-4 [&>svg]:w-4" aria-hidden>{m.icon}</span>{m.label}</div>
                <div className="mt-2 min-h-[2.25rem] text-2xl font-semibold tabular-nums text-nd-text">{m.value}</div>
                <Sparkline points={m.pts} color={m.color} label={`${m.label} — últimas ${range}`} className="mt-2" />
              </Card>
            ))}
          </div>
        )}
      {data && <ProgressBar value={(data.disk.usedGb / data.disk.totalGb) * 100} label="Utilização do disco" />}
    </Section>
  );
}
