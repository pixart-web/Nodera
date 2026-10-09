"use client";

import { useEffect, useState } from "react";
import { ActivityPanel, MetricCards, QuickActions, RecentProjects, ServerUsage, ServicesStatus, type Totals } from "@/components/dashboard/Panels";
import { PageHeader } from "@/components/ui/PageHeader";
import { NotConnected } from "@/components/ui/Feedback";

function useClock() {
  const [now, setNow] = useState<Date | null>(null);
  useEffect(() => { setNow(new Date()); const t = setInterval(() => setNow(new Date()), 30_000); return () => clearInterval(t); }, []);
  return now;
}

export default function DashboardPage() {
  const now = useClock();
  const [totals, setTotals] = useState<Totals | null>(null);

  useEffect(() => {
    // Demo totals (mock) — replace with a real aggregate endpoint.
    setTotals({ projects: 12, wordpress: 8, databases: 14, domains: 18, certificates: 15 });
  }, []);

  const date = now?.toLocaleDateString("pt-PT", { weekday: "long", day: "numeric", month: "long", year: "numeric" });
  const time = now?.toLocaleTimeString("pt-PT", { hour: "2-digit", minute: "2-digit" });

  return (
    <div className="space-y-6">
      <PageHeader title="Bem-vindo à Nodera" description="Gestão centralizada da tua infraestrutura, sites e aplicações."
        actions={<div className="text-right" aria-live="off"><p className="text-sm capitalize text-nd-muted">{date ?? " "}</p><p className="text-3xl font-semibold tabular-nums text-nd-text">{time ?? "--:--"}</p></div>} />
      <NotConnected what="Os números, projetos, atividade e métricas desta página são dados de demonstração." />
      <MetricCards totals={totals} />
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_470px]">
        <div className="min-w-0 space-y-6">
          <RecentProjects />
          <ServerUsage />
        </div>
        <div className="space-y-6">
          <QuickActions />
          <ActivityPanel />
          <ServicesStatus />
        </div>
      </div>
    </div>
  );
}
