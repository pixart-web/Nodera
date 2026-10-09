"use client";

import Link from "next/link";
import { Activity, AppWindow, Server, Wrench } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { Section, StatCard } from "@/components/ui/Card";
import { EmptyState, LoadingState } from "@/components/ui/Feedback";
import type { Application, AuditRecord, Job, Node as NoderaNode, Organization, Page } from "@/lib/types";

// A page's own item count understates the true total once has_more is
// true — "50+" is the honest thing to show rather than a number that
// looks precise but isn't (rule 36).
function countLabel(page: Page<unknown> | null, loading: boolean): string {
  if (loading || !page) return "…";
  return page.has_more ? `${page.items.length}+` : String(page.items.length);
}

export default function OperationsPage() {
  const org = useApi(() => api.get<Organization>("/api/v1/organization"), []);
  const nodes = useApi(() => api.get<Page<NoderaNode>>("/api/v1/infrastructure/nodes"), []);
  const apps = useApi(() => api.get<Page<Application>>("/api/v1/applications"), []);
  const jobs = useApi(() => api.get<Page<Job>>("/api/v1/jobs"), []);
  const audit = useApi(() => api.get<Page<AuditRecord>>("/api/v1/audit"), []);

  const failedJobs = (jobs.data?.items ?? []).filter((j) => j.status === "failed").length;
  const recentAudit = (audit.data?.items ?? []).slice(0, 8);

  return (
    <div>
      <PageHeader title={org.data ? org.data.name : "Visão geral"} description={org.data ? `/${org.data.slug} · dados reais da API Nodera` : "Dados reais da API Nodera"} />

      {(nodes.error || apps.error || jobs.error || audit.error) && (
        <ErrorBanner message="Alguns dados não puderam ser carregados — verifica se a API da Nodera está a correr." />
      )}

      <div className="mb-6 grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard icon={<Server />} tone="blue" value={countLabel(nodes.data, nodes.loading)} label="Nodes" href="/infrastructure" />
        <StatCard icon={<AppWindow />} tone="cyan" value={countLabel(apps.data, apps.loading)} label="Aplicações" href="/applications" />
        <StatCard icon={<Wrench />} tone="purple" value={countLabel(jobs.data, jobs.loading)} label="Jobs" href="/jobs" />
        <StatCard icon={<Activity />} tone={failedJobs > 0 ? "orange" : "green"} value={jobs.loading ? "…" : failedJobs} label="Jobs falhados" href="/jobs" />
      </div>

      <Section title="Atividade recente" href="/audit" linkLabel="Ver tudo">
        {audit.loading ? <LoadingState /> : recentAudit.length === 0 ? <EmptyState icon={<Activity />} title="Ainda sem atividade registada" /> : (
          <div className="-mx-5 overflow-x-auto">
            <table className="data-table">
              <thead><tr><th>Ação</th><th>Recurso</th><th>Autor</th><th>Quando</th></tr></thead>
              <tbody>
                {recentAudit.map((a) => (
                  <tr key={a.id}>
                    <td className="font-mono text-xs">{a.action}</td>
                    <td className="text-xs text-nd-muted">{a.resource_type}</td>
                    <td className="text-xs text-nd-muted">{a.actor_label}</td>
                    <td className="text-xs text-nd-faint">{new Date(a.created_at).toLocaleString("pt-PT")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Section>
      <p className="mt-4 text-xs text-nd-faint">Precisas de ver o dashboard visual? <Link href="/dashboard" className="text-nd-primary-soft hover:underline">Abrir Dashboard</Link></p>
    </div>
  );
}
