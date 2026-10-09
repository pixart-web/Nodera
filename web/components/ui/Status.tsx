import type { ReactNode } from "react";
import { STATUS } from "@/lib/status";
import type { ResourceStatus } from "@/lib/domain";
import { cn } from "./cn";

export function StatusDot({ status, className }: { status: ResourceStatus; className?: string }) {
  const t = STATUS[status];
  return <span aria-hidden className={cn("inline-block h-2 w-2 rounded-full", t.dot, t.pulse && "animate-[nd-pulse_1.6s_ease-in-out_infinite]", className)} />;
}

export function StatusBadge({ status, label }: { status: ResourceStatus; label?: string }) {
  const t = STATUS[status];
  return (
    <span title={t.tooltip} className={cn("inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border px-2.5 py-0.5 text-xs font-medium", t.text, t.bg, t.ring)}>
      <StatusDot status={status} />
      {label ?? t.label}
    </span>
  );
}

// Plain text status (dot + colour, no pill) — used inside dense lists.
export function StatusText({ status, label }: { status: ResourceStatus; label?: string }) {
  const t = STATUS[status];
  return (
    <span title={t.tooltip} className={cn("inline-flex items-center gap-2 whitespace-nowrap text-sm", t.text)}>
      <StatusDot status={status} />
      {label ?? t.label}
    </span>
  );
}

export function Badge({ children, tone = "neutral", className }: { children: ReactNode; tone?: "neutral" | "blue" | "purple" | "green" | "orange" | "red"; className?: string }) {
  const tones = {
    neutral: "bg-nd-hover text-nd-muted border-nd-border",
    blue: "bg-nd-primary/10 text-nd-primary-soft border-nd-primary/25",
    purple: "bg-nd-secondary/10 text-nd-secondary border-nd-secondary/25",
    green: "bg-nd-success/10 text-nd-success border-nd-success/25",
    orange: "bg-nd-warning/10 text-nd-warning border-nd-warning/25",
    red: "bg-nd-danger/10 text-nd-danger border-nd-danger/25",
  };
  return <span className={cn("inline-flex items-center gap-1 whitespace-nowrap rounded-md border px-2 py-0.5 text-xs font-medium", tones[tone], className)}>{children}</span>;
}
