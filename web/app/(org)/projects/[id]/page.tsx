"use client";

import { use, useState } from "react";
import Link from "next/link";
import { Activity, AppWindow, ArrowLeft, Boxes, Database, ExternalLink, Globe, Layers, LayoutGrid, LockKeyhole, KeyRound, Rocket, ScrollText, Settings, Cloud, Siren, Play } from "lucide-react";
import { Breadcrumb } from "@/components/ui/Breadcrumb";
import { PageHeader } from "@/components/ui/PageHeader";
import { Button } from "@/components/ui/Button";
import { Card, Section } from "@/components/ui/Card";
import { TabPanel, Tabs } from "@/components/ui/Tabs";
import { Badge, StatusBadge, StatusText } from "@/components/ui/Status";
import { Alert, EmptyState, ErrorState, LoadingState } from "@/components/ui/Feedback";
import { Avatar } from "@/components/ui/Avatar";
import { Field, Input, Select } from "@/components/ui/Forms";
import { FormModal } from "@/components/ui/FormModal";
import { usePlatform } from "@/components/providers/Platform";
import { useOperations } from "@/components/providers/Operations";
import { useToast } from "@/components/ui/Toast";
import { api, ApiError } from "@/lib/api";
import { toStatus } from "@/lib/status";
import { ago, bytes, date, dateTime } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { backupsService, certificatesService, deploymentsService, domainsService, migrationsService, monitoringService, projectsService, logsService } from "@/services";
import { toProject } from "@/services";
import type { ApiProject, SecretMeta } from "@/lib/types";

const TABS = [
  { id: "overview", label: "Overview", icon: <LayoutGrid className="h-4 w-4" /> },
  { id: "applications", label: "Aplicações", icon: <AppWindow className="h-4 w-4" /> },
  { id: "containers", label: "Containers", icon: <Layers className="h-4 w-4" /> },
  { id: "domains", label: "Domínios", icon: <Globe className="h-4 w-4" /> },
  { id: "ssl", label: "SSL", icon: <LockKeyhole className="h-4 w-4" /> },
  { id: "database", label: "Base de dados", icon: <Database className="h-4 w-4" /> },
  { id: "backups", label: "Backups", icon: <Database className="h-4 w-4" /> },
  { id: "deployments", label: "Deployments", icon: <Rocket className="h-4 w-4" /> },
  { id: "migrations", label: "Migrações", icon: <Cloud className="h-4 w-4" /> },
  { id: "monitoring", label: "Monitorização", icon: <Activity className="h-4 w-4" /> },
  { id: "logs", label: "Logs", icon: <ScrollText className="h-4 w-4" /> },
  { id: "secrets", label: "Secrets", icon: <KeyRound className="h-4 w-4" /> },
  { id: "activity", label: "Atividade", icon: <Siren className="h-4 w-4" /> },
  { id: "settings", label: "Definições", icon: <Settings className="h-4 w-4" /> },
];

function List<T extends { id: string }>({ state, empty, children }: { state: { data: T[] | null; loading: boolean; error: string | null; reload: () => void }; empty: string; children: (rows: T[]) => React.ReactNode }) {
  if (state.loading) return <LoadingState />;
  if (state.error) return <ErrorState message={state.error} onRetry={state.reload} />;
  if (!state.data || state.data.length === 0) return <EmptyState title={empty} />;
  return <ul className="divide-y divide-nd-border/50">{children(state.data)}</ul>;
}
const Row = ({ children }: { children: React.ReactNode }) => <li className="flex flex-wrap items-center justify-between gap-3 py-3 text-sm">{children}</li>;

export default function ProjectDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const { can } = usePlatform();
  const ops = useOperations();
  const toast = useToast();
  const [tab, setTab] = useState("overview");
  const project = useAsync(() => projectsService.get(id), [id]);
  const overview = useAsync(() => projectsService.overview(id), [id]);
  const [edit, setEdit] = useState<{ name: string; description: string } | null>(null);
  const [policy, setPolicy] = useState({ schedule: "daily", type: "full", days: "30" });

  if (project.loading) return <LoadingState rows={3} />;
  if (project.error) return <ErrorState message={project.error} onRetry={project.reload} />;
  const raw: ApiProject | null = project.data;
  if (!raw) return <EmptyState title="Projeto não encontrado" />;
  const p = toProject(raw);
  const active = raw.status === "active";
  const refreshAll = () => { project.reload(); overview.reload(); };
  const isWP = raw.kind === "wordpress";

  return (
    <div>
      <Breadcrumb items={[{ label: "Projetos", href: "/projects" }, { label: p.name }]} />
      <PageHeader
        title={<span className="flex items-center gap-3"><Avatar initial={p.initial} color={p.color} size={44} />{p.name}</span>}
        description={`${raw.slug}${p.domain !== "—" ? " · " + p.domain : ""}${p.clientName ? " · " + p.clientName : ""}`}
        meta={<StatusBadge status={p.status} label={p.statusLabel} />}
        actions={<>
          {p.domain !== "—" && <a href={`https://${p.domain}`} target="_blank" rel="noreferrer"><Button icon={<ExternalLink className="h-4 w-4" />}>Abrir</Button></a>}
          {(raw.status === "provisioning" || raw.status === "failed") && can("projects.create") && <Button variant="primary" icon={<Play className="h-4 w-4" />} onClick={() => ops.submit("Provisionar projeto", () => projectsService.provision(id), refreshAll)}>{raw.status === "failed" ? "Tentar de novo" : "Provisionar"}</Button>}
          {active && can("deployments.create") && <Link href="/deployment"><Button variant="primary" icon={<Rocket className="h-4 w-4" />}>Deploy</Button></Link>}
          {active && can("backups.manage") && <Button icon={<Database className="h-4 w-4" />} onClick={() => ops.submit("Backup", () => backupsService.create(id, { type: "full" }), refreshAll)}>Backup</Button>}
        </>} />
      {raw.status === "failed" && <Alert tone="error" title="O provisionamento falhou" className="mt-4">As alterações foram revertidas. Vê a atividade para o motivo e tenta de novo.</Alert>}
      <div className="mt-5"><Tabs label="Secções do projeto" tabs={TABS} active={tab} onChange={setTab} /></div>

      <TabPanel id="overview" active={tab}>
        {overview.loading ? <LoadingState rows={2} /> : overview.error ? <ErrorState message={overview.error} onRetry={overview.reload} /> : (
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            {Object.entries({ Aplicações: "applications", Domínios: "domains", Certificados: "certificates", Backups: "backups", Deployments: "deployments", Migrações: "migrations", Monitores: "monitors", "Incidentes abertos": "incidents" }).map(([label, key]) => (
              <Card key={key} className="p-4"><p className="text-xs text-nd-muted">{label}</p><p className="mt-1.5 text-2xl font-semibold tabular-nums text-nd-text">{overview.data!.counts[key] ?? 0}</p></Card>))}
            <Card className="p-4 md:col-span-2"><p className="text-xs text-nd-muted">Última atividade</p><p className="mt-1.5 text-lg font-semibold text-nd-text">{ago(overview.data!.last_activity)}</p></Card>
          </div>)}
        {isWP && active && <WPHealth id={id} />}
      </TabPanel>

      <TabPanel id="applications" active={tab}><AppsTab id={id} /></TabPanel>
      <TabPanel id="containers" active={tab}><ContainersTab id={id} /></TabPanel>
      <TabPanel id="domains" active={tab}><DomainsTab id={id} /></TabPanel>
      <TabPanel id="ssl" active={tab}><SslTab id={id} can={can("ssl.issue")} onChange={refreshAll} /></TabPanel>
      <TabPanel id="database" active={tab}><DbTab id={id} /></TabPanel>
      <TabPanel id="backups" active={tab}><BackupsTab id={id} policy={policy} setPolicy={setPolicy} canManage={can("backups.manage")} /></TabPanel>
      <TabPanel id="deployments" active={tab}><DeploymentsTab id={id} /></TabPanel>
      <TabPanel id="migrations" active={tab}><MigrationsTab id={id} /></TabPanel>
      <TabPanel id="monitoring" active={tab}><MonitoringTab id={id} /></TabPanel>
      <TabPanel id="logs" active={tab}><LogsTab id={id} /></TabPanel>
      <TabPanel id="secrets" active={tab}><SecretsTab slug={raw.slug} /></TabPanel>
      <TabPanel id="activity" active={tab}><ActivityTab id={id} /></TabPanel>
      <TabPanel id="settings" active={tab}>
        <Section title="Definições do projeto">
          <dl className="grid gap-3 text-sm sm:grid-cols-2"><div><dt className="text-nd-muted">Slug</dt><dd className="font-mono">{raw.slug}</dd></div><div><dt className="text-nd-muted">Tipo</dt><dd>{raw.kind}</dd></div><div><dt className="text-nd-muted">Criado</dt><dd>{dateTime(raw.created_at)}</dd></div><div><dt className="text-nd-muted">Descrição</dt><dd>{raw.description || "—"}</dd></div></dl>
          {can("projects.update") && <Button className="mt-4" onClick={() => setEdit({ name: raw.name, description: raw.description })}>Editar</Button>}
          {can("projects.delete") && <p className="mt-4 text-xs text-nd-faint">Para eliminar o projeto usa a lista de projetos: a eliminação exige aprovação.</p>}
        </Section>
      </TabPanel>

      <FormModal open={!!edit} onClose={() => setEdit(null)} title="Editar projeto" submitLabel="Guardar" disabled={!edit?.name.trim()}
        onSubmit={async () => { await projectsService.update(id, { name: edit!.name, description: edit!.description }); toast.push("success", "Projeto atualizado."); refreshAll(); }}>
        <Field label="Nome">{(i) => <Input id={i} value={edit?.name ?? ""} onChange={(e) => setEdit({ ...edit!, name: e.target.value })} required />}</Field>
        <Field label="Descrição">{(i) => <Input id={i} value={edit?.description ?? ""} onChange={(e) => setEdit({ ...edit!, description: e.target.value })} />}</Field>
      </FormModal>
      <div className="mt-6"><Link href="/projects" className="inline-flex items-center gap-1.5 text-sm text-nd-muted hover:text-nd-text"><ArrowLeft className="h-4 w-4" />Voltar aos projetos</Link></div>
    </div>
  );
}

function WPHealth({ id }: { id: string }) {
  const h = useAsync(() => projectsService.wpHealth(id), [id]);
  return (
    <Section title="Saúde do WordPress" description={h.data ? `Pontuação ${h.data.score}/100` : undefined}>
      {h.loading ? <LoadingState /> : h.error ? <ErrorState message={h.error} onRetry={h.reload} /> : (
        <ul className="divide-y divide-nd-border/50">{h.data!.checks.map((c) => <li key={c.id} className="flex items-start justify-between gap-3 py-2.5 text-sm"><span>{c.title}{c.detail && <span className="block text-xs text-nd-muted">{c.detail}</span>}</span><Badge tone={c.status === "PASS" ? "green" : c.status === "WARNING" ? "orange" : "red"}>{c.status}</Badge></li>)}</ul>)}
    </Section>
  );
}
function AppsTab({ id }: { id: string }) {
  const s = useAsync(() => projectsService.applications(id), [id]);
  return <Section title="Aplicações"><List state={s} empty="Sem aplicações registadas">{(rows) => rows.map((a) => <Row key={a.id}><span className="font-mono">{a.name}</span><Badge>{a.kind}</Badge><span className="text-nd-muted">{a.environment}</span><StatusText {...toStatus(a.status)} /></Row>)}</List></Section>;
}
function ContainersTab({ id }: { id: string }) {
  const s = useAsync(() => projectsService.containers(id), [id]);
  return (
    <Section title="Containers" description="Estado atual segundo o provider de containers.">
      {s.loading ? <LoadingState /> : s.error ? <ErrorState message={s.error} onRetry={s.reload} /> : !s.data!.available ? <Alert tone="info">Este ambiente não tem provider de containers configurado.</Alert>
        : s.data!.containers.length === 0 ? <EmptyState title="Sem containers" description="O projeto ainda não foi provisionado." />
        : <ul className="divide-y divide-nd-border/50">{s.data!.containers.map((c) => <Row key={c.id}><span className="font-mono">{c.name}</span><span className="text-xs text-nd-muted">{c.image}</span><StatusText {...toStatus(c.state)} /><span className="text-xs text-nd-muted">{c.started_at ? `desde ${ago(c.started_at)}` : ""}</span></Row>)}</ul>}
    </Section>
  );
}
function DomainsTab({ id }: { id: string }) {
  const s = useAsync(() => domainsService.list(id), [id]);
  return <Section title="Domínios" href="/domains" linkLabel="Gerir"><List state={s} empty="Sem domínios associados">{(rows) => rows.map((d) => <Row key={d.id}><span className="font-semibold">{d.name}</span><StatusText {...toStatus(d.dns_status === "ok" ? "ok" : "pending")} label={d.dns_status === "ok" ? "DNS OK" : "A propagar"} /><span className="text-xs text-nd-muted">SSL: {d.ssl_status}</span></Row>)}</List></Section>;
}
function SslTab({ id, can, onChange }: { id: string; can: boolean; onChange: () => void }) {
  const ops = useOperations();
  const s = useAsync(() => certificatesService.list(id), [id]);
  return <Section title="Certificados" href="/ssl" linkLabel="Gerir"><List state={s} empty="Sem certificados">{(rows) => rows.map((c) => <Row key={c.id}><span className="font-semibold">{c.domain}</span><StatusText {...toStatus(c.status)} /><span className="text-xs text-nd-muted">expira {date(c.not_after)} ({c.days_left ?? "—"} dias)</span>{can && !["revoked", "expired", "error"].includes(c.status) && <Button size="sm" onClick={() => ops.submit(`Renovar ${c.domain}`, () => certificatesService.renew(c.domain_id), () => { s.reload(); onChange(); })}>Renovar</Button>}</Row>)}</List></Section>;
}
function DbTab({ id }: { id: string }) {
  const s = useAsync(() => projectsService.databases(id), [id]);
  return <Section title="Bases de dados" description="As credenciais ficam cifradas no cofre de secrets e nunca são mostradas aqui."><List state={s} empty="Sem bases de dados">{(rows) => rows.map((d) => <Row key={d.id}><span className="font-mono">{d.name}</span><Badge>{d.engine}</Badge><span className="text-xs text-nd-muted">utilizador {d.username}</span><StatusText {...toStatus(d.status)} /></Row>)}</List></Section>;
}
function BackupsTab({ id, policy, setPolicy, canManage }: { id: string; policy: { schedule: string; type: string; days: string }; setPolicy: (p: { schedule: string; type: string; days: string }) => void; canManage: boolean }) {
  const ops = useOperations();
  const toast = useToast();
  const b = useAsync(() => backupsService.list(id), [id]);
  const pol = useAsync(() => backupsService.policies(id), [id]);
  return (
    <div className="space-y-6">
      <Section title="Backups" href="/backups" linkLabel="Todos">
        <List state={b} empty="Sem backups">{(rows) => rows.slice(0, 8).map((x) => <Row key={x.id}><Badge>{x.type}</Badge><span>{bytes(x.size_bytes)}</span><StatusText {...toStatus(x.status)} /><span className="text-xs text-nd-muted">{ago(x.created_at)}</span></Row>)}</List>
      </Section>
      <Section title="Políticas agendadas" description="Um backup por período, mesmo com vários servidores a correr.">
        <List state={pol} empty="Sem políticas">{(rows) => rows.map((x) => <Row key={x.id}><span>{x.schedule} · {x.type}</span><span className="text-xs text-nd-muted">retenção {x.retention_days} d</span><span className="text-xs text-nd-muted">última: {ago(x.last_run_at)}</span>{canManage && <Button size="sm" variant="danger" onClick={async () => { try { await backupsService.deletePolicy(x.id); pol.reload(); } catch (e) { toast.push("error", e instanceof ApiError ? e.message : "Falhou"); } }}>Remover</Button>}</Row>)}</List>
        {canManage && (
          <form className="mt-4 grid gap-3 border-t border-nd-border pt-4 sm:grid-cols-4" onSubmit={async (e) => { e.preventDefault(); try { await backupsService.savePolicy(id, { schedule: policy.schedule, type: policy.type, retention_days: Number(policy.days) || 30 }); pol.reload(); } catch (err) { toast.push("error", err instanceof ApiError ? err.message : "Falhou"); } }}>
            <Field label="Frequência">{(i) => <Select id={i} value={policy.schedule} onChange={(e) => setPolicy({ ...policy, schedule: e.target.value })}><option value="daily">diária</option><option value="weekly">semanal</option><option value="monthly">mensal</option></Select>}</Field>
            <Field label="Tipo">{(i) => <Select id={i} value={policy.type} onChange={(e) => setPolicy({ ...policy, type: e.target.value })}>{["full", "database", "files", "media", "configuration"].map((t) => <option key={t}>{t}</option>)}</Select>}</Field>
            <Field label="Retenção (dias)">{(i) => <Input id={i} inputMode="numeric" value={policy.days} onChange={(e) => setPolicy({ ...policy, days: e.target.value })} />}</Field>
            <div className="flex items-end gap-2"><Button type="submit" variant="primary">Guardar política</Button><Button type="button" onClick={() => ops.submit("Backup", () => backupsService.create(id, { type: policy.type }), b.reload)}>Backup agora</Button></div>
          </form>)}
      </Section>
    </div>
  );
}
function DeploymentsTab({ id }: { id: string }) {
  const s = useAsync(() => deploymentsService.list(id), [id]);
  return <Section title="Deployments" href="/deployment" linkLabel="Novo"><List state={s} empty="Nenhum deployment encontrado">{(rows) => rows.map((d) => <Row key={d.id}><span>{d.source === "upload" ? "upload" : `${d.repository}@${d.ref}`}{d.commit_sha ? ` · ${d.commit_sha.slice(0, 7)}` : ""}</span><Badge tone={d.environment === "production" ? "orange" : "neutral"}>{d.environment}</Badge><StatusText {...toStatus(d.status)} /><span className="text-xs text-nd-muted">{ago(d.created_at)}</span></Row>)}</List></Section>;
}
function MigrationsTab({ id }: { id: string }) {
  const s = useAsync(() => migrationsService.list(id), [id]);
  return <Section title="Migrações" href="/migration" linkLabel="Nova"><List state={s} empty="Sem migrações">{(rows) => rows.map((m) => <Row key={m.id}><span className="font-semibold">{m.target_domain}</span><Badge>{m.source_kind}</Badge><StatusText {...toStatus(m.status)} />{m.health_score != null && <span className="text-xs">{m.health_score}/100</span>}</Row>)}</List></Section>;
}
function MonitoringTab({ id }: { id: string }) {
  const s = useAsync(async () => (await monitoringService.monitors()).filter((m) => m.project_id === id), [id]);
  return <Section title="Monitores do projeto" href="/monitoring" linkLabel="Gerir"><List state={s} empty="Sem monitores neste projeto">{(rows) => rows.map((m) => <Row key={m.id}><span className="font-semibold">{m.name}</span><Badge>{m.kind}</Badge><StatusText {...toStatus(m.last_status)} /><span className="text-xs text-nd-muted">{ago(m.last_checked_at)}</span></Row>)}</List></Section>;
}
function LogsTab({ id }: { id: string }) {
  const logs = useAsync(() => logsService.query({ project_id: id, limit: 100 }), [id]);
  const live = useAsync(() => projectsService.containerLogs(id).catch(() => [] as string[]), [id]);
  return (
    <div className="space-y-6">
      <Section title="Registos do projeto" actions={<Button size="sm" onClick={logs.reload}>Atualizar</Button>}>
        {logs.loading ? <LoadingState /> : logs.error ? <ErrorState message={logs.error} onRetry={logs.reload} /> : (logs.data ?? []).length === 0 ? <EmptyState title="Sem registos" /> : (
          <div className="nd-scroll max-h-72 overflow-auto rounded-nd bg-[#050C17] p-3 font-mono text-xs leading-6" role="log" tabIndex={0} aria-label="Registos do projeto">{logs.data!.map((l) => <div key={l.id}><span className="text-nd-faint">[{new Date(l.at).toLocaleTimeString("pt-PT")}]</span> <span className="uppercase text-nd-info">{l.level}</span> {l.message}</div>)}</div>)}
      </Section>
      <Section title="Saída do container" description="Últimas linhas, com credenciais mascaradas.">
        {live.loading ? <LoadingState /> : (live.data ?? []).length === 0 ? <p className="text-sm text-nd-muted">Sem saída disponível.</p> : <pre className="nd-scroll max-h-60 overflow-auto rounded-nd bg-[#050C17] p-3 font-mono text-xs text-nd-text">{live.data!.join("\n")}</pre>}
      </Section>
    </div>
  );
}
function SecretsTab({ slug }: { slug: string }) {
  const s = useAsync(async () => (await api.get<SecretMeta[]>("/api/v1/secrets")).filter((x) => x.key.startsWith(`project/${slug}/`)).map((x) => ({ ...x, id: x.key })));
  return <Section title="Secrets do projeto" href="/secrets" linkLabel="Cofre" description="Só metadados. Os valores nunca são mostrados nesta página."><List state={s as never} empty="Sem secrets deste projeto">{(rows: Array<{ id: string; key: string; description: string; updated_at: string }>) => rows.map((x) => <Row key={x.id}><span className="font-mono">{x.key}</span><span className="text-xs text-nd-muted">{x.description}</span><span className="text-xs text-nd-muted">{ago(x.updated_at)}</span></Row>)}</List></Section>;
}
function ActivityTab({ id }: { id: string }) {
  const ops = useOperations();
  const s = useAsync(() => projectsService.operations(id), [id], );
  return <Section title="Operações do projeto" description="Cada operação tem passos e registos persistidos."><List state={s} empty="Ainda sem operações">{(rows) => rows.map((o) => <Row key={o.id}><button type="button" className="font-mono text-nd-primary-soft hover:underline" onClick={() => ops.track(o.id, o.operation)}>{o.operation}</button><StatusText {...toStatus(o.status)} />{o.rolled_back && <Badge tone="orange">revertida</Badge>}<span className="text-xs text-nd-muted">{ago(o.created_at)}</span></Row>)}</List></Section>;
}
