import type { HTMLAttributes, ReactNode } from "react";
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { cn } from "./cn";

export function Card({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("rounded-nd-lg border border-nd-border bg-nd-surface shadow-card", className)} {...rest} />;
}

export function SectionHeader({ title, description, href, linkLabel = "Ver todos", actions }: {
  title: string; description?: string; href?: string; linkLabel?: string; actions?: ReactNode;
}) {
  return (
    <div className="mb-4 flex items-start justify-between gap-3">
      <div className="min-w-0">
        <h2 className="text-base font-semibold text-nd-text">{title}</h2>
        {description && <p className="mt-0.5 text-xs text-nd-muted">{description}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {actions}
        {href && (
          <Link href={href} className="inline-flex items-center gap-1 text-xs font-medium text-nd-primary-soft transition-colors hover:text-nd-text">
            {linkLabel} <ArrowRight className="h-3 w-3" aria-hidden />
          </Link>
        )}
      </div>
    </div>
  );
}

export function Section({ className, children, ...header }: React.ComponentProps<typeof SectionHeader> & { className?: string; children: ReactNode }) {
  return (
    <Card className={cn("p-5", className)}>
      <SectionHeader {...header} />
      {children}
    </Card>
  );
}

const TONES = {
  blue: "bg-nd-primary/15 text-nd-primary-soft",
  purple: "bg-nd-secondary/15 text-nd-secondary",
  green: "bg-nd-success/15 text-nd-success",
  orange: "bg-nd-warning/15 text-nd-warning",
  cyan: "bg-nd-info/15 text-nd-info",
} as const;
export type Tone = keyof typeof TONES;

export function IconTile({ icon, tone = "blue", className }: { icon: ReactNode; tone?: Tone; className?: string }) {
  return <div className={cn("flex h-10 w-10 shrink-0 items-center justify-center rounded-nd [&>svg]:h-5 [&>svg]:w-5", TONES[tone], className)} aria-hidden>{icon}</div>;
}

export function StatCard({ icon, tone = "blue", value, label, badge, badgeTone = "green", href }: {
  icon: ReactNode; tone?: Tone; value: ReactNode; label: string; badge?: string; badgeTone?: Tone; href?: string;
}) {
  const body = (
    <Card className="h-full p-4 transition-colors duration-200 hover:border-nd-strong hover:bg-nd-hover">
      <div className="flex items-start justify-between">
        <IconTile icon={icon} tone={tone} />
        {badge && <span className={cn("rounded-md px-1.5 py-0.5 text-[11px] font-semibold", TONES[badgeTone])}>{badge}</span>}
      </div>
      <div className="mt-4 text-3xl font-semibold tracking-tight text-nd-text tabular-nums">{value}</div>
      <div className="mt-0.5 text-sm text-nd-muted">{label}</div>
    </Card>
  );
  return href ? <Link href={href} className="block rounded-nd-lg">{body}</Link> : body;
}
