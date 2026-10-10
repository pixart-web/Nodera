"use client";

import Link from "next/link";
import { Boxes, CloudUpload, Code2, Database, Globe, Box, Layers, Plus, ShieldCheck, Activity, Siren, Server } from "lucide-react";
import { Card, IconTile, Section, StatCard, type Tone } from "@/components/ui/Card";
import { StatusText } from "@/components/ui/Status";
import { Timeline } from "@/components/ui/Timeline";
import { EmptyState, ErrorState, LoadingState, Skeleton } from "@/components/ui/Feedback";
import { ProjectRow } from "@/components/projects/ProjectParts";
import { usePlatform } from "@/components/providers/Platform";
import { toStatus } from "@/lib/status";
import { ago } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { dashboardService, monitoringService, projectsService } from "@/services";
import type { ApiDashboardSummary, ApiOperation } from "@/lib/types";

const sum = (o?: Record<string, number>) => Object.values(o ?? {}).reduce((a, b) => a + b, 0);

export function MetricCards({ data }: { data: ApiDashboardSummary | null }) {
  const bad = (data?.monitors?.failing ?? 0) + (data?.monitors?.warning ?? 0);
  const cards: Array<{ icon: React.ReactNode; tone: Tone; label: string; value: number; badge: string; badgeTone: Tone; href: string; section: string }> = [
    { section: "projects", icon: <Box />, tone: "blue", label: "Projetos", value: sum(data?.projects), badge: `${data?.projects?.active ?? 0} ativos`, badgeTone: "green", href: "/projects" },
    { section: "certificates", icon: <ShieldCheck />, tone: "green", label: "Certificados", value: sum(data?.certificates), badge: `${data?.certificates?.valid ?? 0} válidos`, badgeTone: "green", href: "/ssl" },
    { section: "backups", icon: <Database />, tone: "purple", label: "Backups (30 d)", value: sum(data?.backups), badge: `${data?.backups?.completed ?? 0} completos`, badgeTone: "purple", href: "/backups" },
    { section: "monitors", icon: <Activity />, tone: bad ? "orange" : "cyan", label: "Monitores", value: sum(data?.monitors), badge: bad ? `${bad} com problemas` : "todos OK", badgeTone: bad ? "orange" : "green", href: "/monitoring" },
    { section: "incidents", icon: <Siren />, tone: data?.open_incidents ? "orange" : "green", label: "Incidentes abertos", value: data?.open_incidents ?? 0, badge: data?.open_incidents ? "requer atenção" : "nenhum", badgeTone: data?.open_incidents ? "orange" : "green", href: "/monitoring" },
  ];
  const visible = data ? cards.filter((c) => data.sections.includes(c.section)) : cards;
  return (
    <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-5">
      {visible.map((c) => data
        ? <StatCard key={c.label} icon={c.icon} tone={c.tone} value={c.value} label={c.label} badge={c.badge} badgeTone={c.badgeTone} href={c.href} />
        : <Skeleton key={c.label} className="h-[148px] rounded-nd-lg" />)}
    </div>
  );
}

export function QuickActions() {
  const { can } = usePlatform();
  const actions: Array<{ icon: React.ReactNode; tone: Tone; title: string; desc: string; href: string; perm?: string }> = [
    { icon: <Boxes />, tone: "blue", title: "Novo WordPress", desc: "Criar e provisionar", href: "/projects?new=wordpress", perm: "projects.create" },
    { icon: <CloudUpload />, tone: "cyan", title: "Migrar WordPress", desc: "A partir de um backup ZIP", href: "/migration", perm: "migrations.create" },
    { icon: <Code2 />, tone: "blue", title: "Deployment", desc: "Publicar uma versão", href: "/deployment", perm: "deployments.create" },
    { icon: <Globe />, tone: "green", title: "Adicionar Domínio", desc: "Zona DNS", href: "/domains?new=1", perm: "domains.manage" },
    { icon: <ShieldCheck />, tone: "green", title: "Emitir SSL", desc: "Certificado", href: "/ssl", perm: "ssl.issue" },
    { icon: <Database />, tone: "purple", title: "Criar Backup", desc: "Verificado por checksum", href: "/backups?new=1", perm: "backups.manage" },
  ];
  const cls = "group flex h-full items-center gap-3 rounded-nd-lg border border-nd-border bg-nd-elevated p-3 text-left transition-all duration-200 hover:-translate-y-px hover:border-nd-strong hover:bg-nd-hover";
  const shown = actions.filter((a) => !a.perm || can(a.perm));
  return (
    <Section title="Ações Rápidas">
      {shown.length === 0 ? <p className="text-sm text-nd-muted">Não tens permissões para ações rápidas.</p> : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {shown.map((a) => (
            <Link key={a.title} href={a.href} className={cls}>
              <IconTile icon={a.icon} tone={a.tone} /><span className="min-w-0"><span className="block text-sm font-semibold leading-tight text-nd-text">{a.title}</span><span className="block text-xs text-nd-muted">{a.desc}</span></span>
            </Link>))}
        </div>)}
    </Section>
  );
}

export function RecentProjects() {
  const { data, loading, error, reload } = useAsync(() => projectsService.list({ limit: 6 }));
  return (
    <Section title="Projetos Recentes" href="/projects">
      {loading ? <LoadingState rows={4} />
        : error ? <ErrorState message={error} onRetry={reload} />
        : !data || data.length === 0 ? <EmptyState icon={<Box />} title="Nenhum projeto ainda" description="Cria o teu primeiro projeto para começar." />
        : <div className="-mx-2 divide-y divide-nd-border/50">{data.slice(0, 6).map((p) => <ProjectRow key={p.id} project={p} />)}</div>}
    </Section>
  );
}

export function ActivityPanel({ ops }: { ops?: ApiOperation[] }) {
  return (
    <Section title="Operações Recentes" href="/operations" linkLabel="Ver todas">
      {!ops || ops.length === 0 ? <EmptyState title="Sem operações recentes" description="Provisionar, fazer backups ou deployments cria operações com histórico completo." />
        : <Timeline items={ops.map((o) => { const s = toStatus(o.status); return { id: o.id, icon: <Layers />, status: s.status, title: o.operation, description: o.error || (s.label ?? o.status), time: ago(o.created_at) }; })} />}
    </Section>
  );
}

export function ServicesStatus() {
  const { system } = usePlatform();
  const mons = useAsync(() => monitoringService.monitors());
  const caps = system ? Object.entries(system.capabilities) : [];
  const capTone = (v: string) => (v === "not_configured" ? { status: "OFFLINE" as const, label: "não configurado" } : v === "mock" ? { status: "WARNING" as const, label: "MOCK" } : { status: "ONLINE" as const, label: v === "local" ? "local" : "real" });
  return (
    <Section title="Estado dos Serviços" href="/monitoring" linkLabel="Ver detalhes">
      <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-nd-faint">Providers</p>
      <ul className="mb-4 divide-y divide-nd-border/50">{caps.map(([k, v]) => <li key={k} className="flex items-center justify-between py-2 text-sm"><span className="flex items-center gap-2"><Server className="h-4 w-4 text-nd-muted" aria-hidden />{k}</span><StatusText {...capTone(v)} /></li>)}</ul>
      <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-nd-faint">Monitores</p>
      {mons.loading ? <LoadingState /> : mons.error ? <ErrorState message={mons.error} onRetry={mons.reload} /> : (mons.data ?? []).length === 0 ? <p className="text-sm text-nd-muted">Sem monitores configurados.</p> : (
        <ul className="divide-y divide-nd-border/50">{mons.data!.slice(0, 6).map((m) => <li key={m.id} className="flex items-center justify-between py-2 text-sm"><span className="truncate">{m.name}</span><StatusText {...toStatus(m.last_status)} /></li>)}</ul>)}
    </Section>
  );
}

export function PendingApprovals({ count }: { count: number }) {
  if (!count) return null;
  return (
    <Link href="/tools" className="block"><Card className="flex items-center gap-3 border-nd-warning/40 p-4 hover:bg-nd-hover"><IconTile icon={<Plus />} tone="orange" /><div><p className="text-sm font-semibold text-nd-text">{count} pedido(s) a aguardar aprovação</p><p className="text-xs text-nd-muted">Operações perigosas só correm depois de aprovadas.</p></div></Card></Link>
  );
}
