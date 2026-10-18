"use client";

import { useState } from "react";
import { Activity, Plus, Siren } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Card, Section } from "@/components/ui/Card";
import { PageHeader } from "@/components/ui/PageHeader";
import { AreaChart } from "@/components/ui/Charts";
import { TabPanel, Tabs } from "@/components/ui/Tabs";
import { Badge, StatusText } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { EmptyState, ErrorState, LoadingState, Alert } from "@/components/ui/Feedback";
import { Field, Input, Select } from "@/components/ui/Forms";
import { FormModal } from "@/components/ui/FormModal";
import { Modal } from "@/components/ui/Overlay";
import { usePlatform } from "@/components/providers/Platform";
import { useToast } from "@/components/ui/Toast";
import { ApiError } from "@/lib/api";
import { toStatus } from "@/lib/status";
import { ago, dateTime } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { monitoringService } from "@/services";
import type { ApiAlertRule, ApiIncident, ApiMonitor } from "@/lib/types";

const TABS = [{ id: "monitors", label: "Monitores" }, { id: "incidents", label: "Incidentes" }, { id: "rules", label: "Regras de alerta" }, { id: "metrics", label: "Métricas dos nós" }];
const KINDS = ["http", "tcp", "dns", "ssl", "container", "database"];
const CONDITIONS = ["http_failure", "container_unhealthy", "database_unavailable", "ssl_expiry_days", "cpu_above", "ram_above", "disk_above"];

function MetricChart({ metric, label, unit = "%" }: { metric: string; label: string; unit?: string }) {
  const { data, loading, error } = useAsync(() => monitoringService.metric(metric));
  if (loading) return <LoadingState />;
  if (error) return <ErrorState message={error} />;
  const pts = [...(data ?? [])].reverse().map((s) => s.value);
  if (pts.length === 0) return <EmptyState title="Sem amostras" description="Aparecem quando um Node Agent enviar heartbeats com métricas." />;
  return <><p className="mb-2 text-sm text-nd-muted">Último valor: <span className="font-semibold text-nd-text">{pts[pts.length - 1]?.toFixed(1)}{unit}</span></p><AreaChart series={pts} label={label} unit={unit} max={unit === "%" ? 100 : Math.max(...pts) * 1.2 || 1} /></>;
}

export default function MonitoringPage() {
  const { can } = usePlatform();
  const toast = useToast();
  const [tab, setTab] = useState("monitors");
  const monitors = useAsync(() => monitoringService.monitors());
  const incidents = useAsync(() => monitoringService.incidents());
  const rules = useAsync(() => monitoringService.rules());
  const [newMon, setNewMon] = useState(false);
  const [mf, setMf] = useState({ kind: "http", name: "", target: "", interval: "60" });
  const [newRule, setNewRule] = useState(false);
  const [rf, setRf] = useState({ name: "", condition: "http_failure", threshold: "0", severity: "warning" });
  const [inc, setInc] = useState<ApiIncident | null>(null);
  const manage = can("monitoring.manage"), manageInc = can("incidents.manage");
  const fail = (e: unknown) => toast.push("error", e instanceof ApiError ? e.message : "Falhou");

  const monCols: Column<ApiMonitor>[] = [
    { key: "n", header: "Monitor", primary: true, cell: (m) => <div><p className="font-semibold">{m.name}</p><p className="max-w-[320px] truncate font-mono text-xs text-nd-muted">{m.target}</p></div> },
    { key: "k", header: "Tipo", cell: (m) => <Badge>{m.kind}</Badge> },
    { key: "s", header: "Estado", cell: (m) => <span title={m.last_error}><StatusText {...toStatus(m.last_status)} /></span> },
    { key: "l", header: "Latência", hideOnMobile: true, cell: (m) => <span className="tabular-nums text-nd-muted">{m.last_latency_ms != null ? `${m.last_latency_ms} ms` : "—"}</span> },
    { key: "c", header: "Última verificação", hideOnMobile: true, cell: (m) => <span className="text-nd-muted">{ago(m.last_checked_at)}</span> },
    { key: "a", header: "", hideOnMobile: true, className: "text-right", cell: (m) => manage ? (
      <div className="flex justify-end gap-1.5">
        <Button size="sm" onClick={async () => { try { await monitoringService.runMonitor(m.id); monitors.reload(); } catch (e) { fail(e); } }}>Verificar</Button>
        <Button size="sm" onClick={async () => { try { await monitoringService.toggleMonitor(m.id, !m.enabled); monitors.reload(); } catch (e) { fail(e); } }}>{m.enabled ? "Pausar" : "Retomar"}</Button>
        <Button size="sm" variant="danger" onClick={async () => { try { await monitoringService.deleteMonitor(m.id); monitors.reload(); } catch (e) { fail(e); } }}>Apagar</Button>
      </div>) : null },
  ];
  const incCols: Column<ApiIncident>[] = [
    { key: "t", header: "Incidente", primary: true, cell: (i) => <button type="button" className="text-left font-semibold hover:text-nd-primary-soft" onClick={async () => { try { setInc(await monitoringService.incident(i.id)); } catch (e) { fail(e); } }}>{i.title}</button> },
    { key: "sev", header: "Severidade", cell: (i) => <Badge tone={i.severity === "critical" ? "red" : i.severity === "warning" ? "orange" : "blue"}>{i.severity}</Badge> },
    { key: "st", header: "Estado", cell: (i) => <StatusText {...toStatus(i.status)} /> },
    { key: "d", header: "Detetado", cell: (i) => <span className="text-nd-muted">{ago(i.detected_at)}</span> },
  ];
  const ruleCols: Column<ApiAlertRule>[] = [
    { key: "n", header: "Regra", primary: true, cell: (r) => <span className="font-semibold">{r.name}</span> },
    { key: "c", header: "Condição", cell: (r) => <span className="font-mono text-xs">{r.condition}{r.threshold ? ` > ${r.threshold}` : ""}</span> },
    { key: "s", header: "Severidade", cell: (r) => <Badge tone={r.severity === "critical" ? "red" : "orange"}>{r.severity}</Badge> },
    { key: "a", header: "", className: "text-right", hideOnMobile: true, cell: (r) => manage ? <Button size="sm" variant="danger" onClick={async () => { try { await monitoringService.deleteRule(r.id); rules.reload(); } catch (e) { fail(e); } }}>Apagar</Button> : null },
  ];
  const openCount = (incidents.data ?? []).filter((i) => !["resolved", "closed"].includes(i.status)).length;

  return (
    <div className="space-y-6">
      <PageHeader title="Monitorização" description="Verificações reais, alertas e incidentes. Os alertas abrem no máximo um incidente por problema."
        actions={tab === "monitors" && manage ? <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setNewMon(true)}>Novo monitor</Button> : tab === "rules" && manage ? <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setNewRule(true)}>Nova regra</Button> : undefined} />
      <Tabs label="Secções" tabs={TABS.map((t) => t.id === "incidents" ? { ...t, label: `Incidentes${openCount ? ` (${openCount})` : ""}` } : t)} active={tab} onChange={setTab} />

      <TabPanel id="monitors" active={tab}>
        <Card className="p-5"><DataTable caption="Monitores" columns={monCols} rows={monitors.data ?? []} loading={monitors.loading} error={monitors.error} onRetry={monitors.reload}
          empty={{ icon: <Activity />, title: "Sem monitores", description: "Cria um monitor HTTP, TCP, DNS, SSL, container ou base de dados.", action: manage ? { label: "Novo monitor", onClick: () => setNewMon(true) } : undefined }} /></Card>
      </TabPanel>
      <TabPanel id="incidents" active={tab}>
        <Card className="p-5"><DataTable caption="Incidentes" columns={incCols} rows={incidents.data ?? []} loading={incidents.loading} error={incidents.error} onRetry={incidents.reload}
          empty={{ icon: <Siren />, title: "Sem incidentes", description: "Tudo calmo. Os incidentes aparecem quando uma regra de alerta dispara." }} /></Card>
      </TabPanel>
      <TabPanel id="rules" active={tab}>
        <Card className="p-5"><DataTable caption="Regras de alerta" columns={ruleCols} rows={rules.data ?? []} loading={rules.loading} error={rules.error} onRetry={rules.reload}
          empty={{ title: "Sem regras de alerta", description: "Sem regras, nenhum incidente é aberto automaticamente.", action: manage ? { label: "Nova regra", onClick: () => setNewRule(true) } : undefined }} /></Card>
      </TabPanel>
      <TabPanel id="metrics" active={tab}>
        <div className="grid gap-6 lg:grid-cols-3">
          <Section title="CPU"><MetricChart metric="cpu" label="CPU" /></Section>
          <Section title="Memória"><MetricChart metric="ram" label="RAM" /></Section>
          <Section title="Disco"><MetricChart metric="disk" label="Disco" /></Section>
        </div>
        <Alert tone="info" className="mt-4" title="De onde vêm estes dados">São amostras enviadas pelos Node Agents nos heartbeats. Sem agente ligado não há amostras — e não são inventadas.</Alert>
      </TabPanel>

      <FormModal open={newMon} onClose={() => setNewMon(false)} title="Novo monitor" submitLabel="Criar" disabled={!mf.name.trim() || !mf.target.trim()}
        onSubmit={async () => { await monitoringService.createMonitor({ kind: mf.kind, name: mf.name, target: mf.target, interval_seconds: Number(mf.interval) || 60 }); setMf({ kind: "http", name: "", target: "", interval: "60" }); monitors.reload(); }}>
        <Field label="Tipo">{(id) => <Select id={id} value={mf.kind} onChange={(e) => setMf({ ...mf, kind: e.target.value })}>{KINDS.map((k) => <option key={k}>{k}</option>)}</Select>}</Field>
        <Field label="Nome">{(id) => <Input id={id} value={mf.name} onChange={(e) => setMf({ ...mf, name: e.target.value })} required />}</Field>
        <Field label="Alvo" hint={mf.kind === "http" ? "https://exemplo.pt" : mf.kind === "tcp" ? "host:porta" : mf.kind === "container" || mf.kind === "database" ? "nome" : "hostname"}>{(id) => <Input id={id} value={mf.target} onChange={(e) => setMf({ ...mf, target: e.target.value })} required />}</Field>
        <Field label="Intervalo (s)">{(id) => <Input id={id} inputMode="numeric" value={mf.interval} onChange={(e) => setMf({ ...mf, interval: e.target.value })} />}</Field>
        <Alert tone="info">Alvos privados ou de metadados (127.0.0.1, 169.254.169.254, redes internas) são bloqueados pela política SSRF.</Alert>
      </FormModal>
      <FormModal open={newRule} onClose={() => setNewRule(false)} title="Nova regra de alerta" submitLabel="Criar" disabled={!rf.name.trim()}
        onSubmit={async () => { await monitoringService.createRule({ name: rf.name, condition: rf.condition, threshold: Number(rf.threshold) || 0, severity: rf.severity }); setRf({ name: "", condition: "http_failure", threshold: "0", severity: "warning" }); rules.reload(); }}>
        <Field label="Nome">{(id) => <Input id={id} value={rf.name} onChange={(e) => setRf({ ...rf, name: e.target.value })} required />}</Field>
        <Field label="Condição">{(id) => <Select id={id} value={rf.condition} onChange={(e) => setRf({ ...rf, condition: e.target.value })}>{CONDITIONS.map((c) => <option key={c}>{c}</option>)}</Select>}</Field>
        {["cpu_above", "ram_above", "disk_above", "ssl_expiry_days"].includes(rf.condition) && <Field label={rf.condition === "ssl_expiry_days" ? "Dias" : "Limite (%)"}>{(id) => <Input id={id} inputMode="numeric" value={rf.threshold} onChange={(e) => setRf({ ...rf, threshold: e.target.value })} />}</Field>}
        <Field label="Severidade">{(id) => <Select id={id} value={rf.severity} onChange={(e) => setRf({ ...rf, severity: e.target.value })}><option>info</option><option>warning</option><option>critical</option></Select>}</Field>
      </FormModal>

      {inc && (
        <Modal open onClose={() => setInc(null)} title={inc.title} size="lg" description={`${inc.severity} · ${inc.status} · detetado ${dateTime(inc.detected_at)}`}
          footer={<>
            {manageInc && inc.status === "open" && <Button onClick={async () => { try { setInc(await monitoringService.incidentAction(inc.id, "acknowledge")); incidents.reload(); } catch (e) { fail(e); } }}>Reconhecer</Button>}
            {manageInc && ["open", "acknowledged"].includes(inc.status) && <Button onClick={async () => { try { setInc(await monitoringService.incidentAction(inc.id, "investigate")); incidents.reload(); } catch (e) { fail(e); } }}>Investigar</Button>}
            {manageInc && ["open", "acknowledged", "investigating"].includes(inc.status) && <Button onClick={async () => { try { setInc(await monitoringService.incidentAction(inc.id, "resolve")); incidents.reload(); } catch (e) { fail(e); } }}>Resolver</Button>}
            {manageInc && inc.status === "resolved" && <Button onClick={async () => { try { setInc(await monitoringService.incidentAction(inc.id, "close")); incidents.reload(); } catch (e) { fail(e); } }}>Fechar</Button>}
            <Button variant="primary" onClick={() => setInc(null)}>Fechar janela</Button></>}>
          <ol className="space-y-2" aria-label="Cronologia">
            {(inc.events ?? []).map((e, i) => <li key={i} className="flex gap-3 text-sm"><span className="w-28 shrink-0 text-xs text-nd-muted">{ago(e.at)}</span><span><span className="font-medium">{e.kind}</span> — {e.message} <span className="text-xs text-nd-faint">({e.actor})</span></span></li>)}
          </ol>
        </Modal>
      )}
    </div>
  );
}
