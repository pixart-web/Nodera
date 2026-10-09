import type { ResourceStatus } from "./domain";

// Single source of truth for status colour/label/semantics. Components never
// hardcode status colours — they read them from here.
export interface StatusToken {
  label: string;
  tooltip: string;
  text: string; // tailwind text colour class
  bg: string;   // soft background class
  ring: string; // border class
  dot: string;  // solid dot class
  pulse?: boolean;
}

export const STATUS: Record<ResourceStatus, StatusToken> = {
  ONLINE: { label: "Online", tooltip: "Operacional", text: "text-nd-success", bg: "bg-nd-success/10", ring: "border-nd-success/25", dot: "bg-nd-success" },
  WARNING: { label: "Atenção", tooltip: "Requer atenção", text: "text-nd-warning", bg: "bg-nd-warning/10", ring: "border-nd-warning/25", dot: "bg-nd-warning" },
  ERROR: { label: "Erro", tooltip: "Falha detetada", text: "text-nd-danger", bg: "bg-nd-danger/10", ring: "border-nd-danger/25", dot: "bg-nd-danger" },
  OFFLINE: { label: "Offline", tooltip: "Indisponível", text: "text-nd-danger", bg: "bg-nd-danger/10", ring: "border-nd-danger/25", dot: "bg-nd-danger" },
  DEPLOYING: { label: "Deploying", tooltip: "Deployment em curso", text: "text-nd-info", bg: "bg-nd-info/10", ring: "border-nd-info/25", dot: "bg-nd-info", pulse: true },
  MIGRATING: { label: "Migrating", tooltip: "Migração em curso", text: "text-nd-secondary", bg: "bg-nd-secondary/10", ring: "border-nd-secondary/25", dot: "bg-nd-secondary", pulse: true },
  BACKUP: { label: "Backup", tooltip: "Backup em curso", text: "text-nd-primary-soft", bg: "bg-nd-primary/10", ring: "border-nd-primary/25", dot: "bg-nd-primary", pulse: true },
  MAINTENANCE: { label: "Manutenção", tooltip: "Em manutenção", text: "text-nd-warning", bg: "bg-nd-warning/10", ring: "border-nd-warning/25", dot: "bg-nd-warning" },
  PENDING: { label: "A propagar", tooltip: "A aguardar propagação", text: "text-nd-info", bg: "bg-nd-info/10", ring: "border-nd-info/25", dot: "bg-nd-info" },
  UNKNOWN: { label: "Desconhecido", tooltip: "Estado desconhecido", text: "text-nd-muted", bg: "bg-nd-muted/10", ring: "border-nd-muted/25", dot: "bg-nd-muted" },
};
