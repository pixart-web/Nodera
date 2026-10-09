"use client";

import { useState } from "react";
import { Card, Section } from "@/components/ui/Card";
import { PageHeader } from "@/components/ui/PageHeader";
import { AreaChart, ProgressBar } from "@/components/ui/Charts";
import { Segmented } from "@/components/ui/Tabs";
import { ErrorState, LoadingState, NotConnected } from "@/components/ui/Feedback";
import { StatusText } from "@/components/ui/Status";
import { useAsync } from "@/lib/useAsync";
import { metricsService, projectsService, servicesHealthService } from "@/services";
import type { MetricRange } from "@/lib/domain";

const RANGES = ["1h", "6h", "24h", "7d"] as const;

export default function MonitoringPage() {
  const [range, setRange] = useState<MetricRange>("24h");
  const m = useAsync(() => metricsService.get(range), [range]);
  const svc = useAsync(() => servicesHealthService.list());
  const projects = useAsync(() => projectsService.list());
  const pts = (id: string) => m.data?.series.find((s) => s.id === id)?.points ?? [];

  return (
    <div className="space-y-6">
      <PageHeader title="Monitorização" description="Saúde do servidor, containers e serviços." actions={<Segmented label="Período" options={RANGES} value={range} onChange={setRange} />} />
      <NotConnected what="As métricas são séries sintéticas de demonstração." />
      {m.loading ? <LoadingState rows={2} /> : m.error ? <ErrorState message={m.error} onRetry={m.reload} /> : (
        <div className="grid gap-6 lg:grid-cols-2">
          <Section title="CPU" description={`${m.data!.cpu.pct}% agora`}><AreaChart series={pts("cpu")} label={`CPU últimas ${range}`} color="rgb(var(--color-success))" /></Section>
          <Section title="Memória RAM" description={`${m.data!.ram.usedGb} GB / ${m.data!.ram.totalGb} GB`}><AreaChart series={pts("ram")} label={`RAM últimas ${range}`} /></Section>
          <Section title="Disco" description={`${m.data!.disk.usedGb} GB / ${m.data!.disk.totalGb} GB`}><AreaChart series={pts("disk")} label={`Disco últimas ${range}`} color="rgb(var(--color-secondary))" /><div className="mt-3"><ProgressBar label="Disco" value={(m.data!.disk.usedGb / m.data!.disk.totalGb) * 100} /></div></Section>
          <Section title="Rede" description={`↑ ${m.data!.network.upMbps} Mbps · ↓ ${m.data!.network.downMbps} Mbps`}><AreaChart series={pts("network")} label={`Rede últimas ${range}`} unit=" Mbps" max={30} color="rgb(var(--color-warning))" /></Section>
        </div>
      )}
      <div className="grid gap-6 lg:grid-cols-3">
        <Section title="Serviços">{svc.loading ? <LoadingState /> : <ul className="divide-y divide-nd-border/50">{svc.data?.map((s) => <li key={s.id} className="flex items-center justify-between py-2.5 text-sm"><span>{s.name}</span><StatusText status={s.status} /></li>)}</ul>}</Section>
        <Section title="HTTP / SSL">{projects.loading ? <LoadingState /> : <ul className="divide-y divide-nd-border/50">{projects.data?.map((p) => <li key={p.id} className="flex items-center justify-between py-2.5 text-sm"><span className="truncate">{p.domain}</span><StatusText status={p.status} label={p.statusLabel} /></li>)}</ul>}</Section>
        <Card className="p-5"><h2 className="text-base font-semibold">Containers & Bases de dados</h2><p className="mt-2 text-sm text-nd-muted">Disponível em breve — requer o Node Agent, ainda não implementado.</p></Card>
      </div>
    </div>
  );
}
