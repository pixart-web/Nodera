"use client";

import { useEffect, useState } from "react";
import { ActivityPanel, MetricCards, PendingApprovals, QuickActions, RecentProjects, ServicesStatus } from "@/components/dashboard/Panels";
import { PageHeader } from "@/components/ui/PageHeader";
import { ErrorState } from "@/components/ui/Feedback";
import { useAsync } from "@/lib/useAsync";
import { dashboardService } from "@/services";

function useClock() {
  const [now, setNow] = useState<Date | null>(null);
  useEffect(() => { setNow(new Date()); const t = setInterval(() => setNow(new Date()), 30_000); return () => clearInterval(t); }, []);
  return now;
}

export default function DashboardPage() {
  const now = useClock();
  const dash = useAsync(() => dashboardService.get());
  useEffect(() => { const t = setInterval(dash.reload, 30_000); return () => clearInterval(t); }, [dash.reload]);
  const date = now?.toLocaleDateString("pt-PT", { weekday: "long", day: "numeric", month: "long", year: "numeric" });
  const time = now?.toLocaleTimeString("pt-PT", { hour: "2-digit", minute: "2-digit" });

  return (
    <div className="space-y-6">
      <PageHeader title="Bem-vindo à Nodera" description="Gestão centralizada da tua infraestrutura, sites e aplicações."
        actions={<div className="text-right" aria-live="off"><p className="text-sm capitalize text-nd-muted">{date ?? " "}</p><p className="text-3xl font-semibold tabular-nums text-nd-text">{time ?? "--:--"}</p></div>} />
      {dash.error && <ErrorState message={dash.error} onRetry={dash.reload} />}
      <MetricCards data={dash.data} />
      <PendingApprovals count={dash.data?.pending_approvals ?? 0} />
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_470px]">
        <div className="min-w-0 space-y-6"><RecentProjects /></div>
        <div className="space-y-6">
          <QuickActions />
          <ActivityPanel ops={dash.data?.recent_operations} />
          <ServicesStatus />
        </div>
      </div>
    </div>
  );
}
