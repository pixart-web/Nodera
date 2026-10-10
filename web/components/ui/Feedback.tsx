import type { ReactNode } from "react";
import { AlertTriangle, CheckCircle2, Info, Loader2, XCircle, Clock } from "lucide-react";
import { Button } from "./Button";
import { cn } from "./cn";

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden className={cn("animate-[nd-pulse_1.4s_ease-in-out_infinite] rounded-md bg-nd-hover", className)} />;
}

export function LoadingState({ label = "A carregar…", rows = 0 }: { label?: string; rows?: number }) {
  return (
    <div role="status" aria-live="polite" className="space-y-3 py-6">
      <div className="flex items-center justify-center gap-2 text-sm text-nd-muted">
        <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" aria-hidden />{label}
      </div>
      {Array.from({ length: rows }).map((_, i) => <Skeleton key={i} className="h-12 w-full" />)}
    </div>
  );
}

export function EmptyState({ icon, title, description, action }: { icon?: ReactNode; title: string; description?: string; action?: { label: string; onClick?: () => void } }) {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-12 text-center">
      {icon && <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-nd-hover text-nd-muted [&>svg]:h-6 [&>svg]:w-6" aria-hidden>{icon}</div>}
      <p className="text-sm font-medium text-nd-text">{title}</p>
      {description && <p className="mt-1 max-w-sm text-sm text-nd-muted">{description}</p>}
      {action && <Button variant="primary" size="sm" className="mt-4" onClick={action.onClick}>{action.label}</Button>}
    </div>
  );
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div role="alert" className="flex flex-col items-center px-6 py-10 text-center">
      <XCircle className="mb-3 h-8 w-8 text-nd-danger" aria-hidden />
      <p className="text-sm font-medium text-nd-text">Não foi possível carregar os dados</p>
      <p className="mt-1 text-sm text-nd-muted">{message}</p>
      {onRetry && <Button size="sm" className="mt-4" onClick={onRetry}>Tentar novamente</Button>}
    </div>
  );
}

const ALERTS = {
  info: { icon: Info, cls: "border-nd-info/25 bg-nd-info/10 text-nd-info" },
  success: { icon: CheckCircle2, cls: "border-nd-success/25 bg-nd-success/10 text-nd-success" },
  warning: { icon: AlertTriangle, cls: "border-nd-warning/25 bg-nd-warning/10 text-nd-warning" },
  error: { icon: XCircle, cls: "border-nd-danger/25 bg-nd-danger/10 text-nd-danger" },
} as const;

export function Alert({ tone = "info", title, children, className }: { tone?: keyof typeof ALERTS; title?: string; children?: ReactNode; className?: string }) {
  const { icon: Icon, cls } = ALERTS[tone];
  return (
    <div role={tone === "error" ? "alert" : "status"} className={cn("flex gap-3 rounded-nd border p-3 text-sm", cls, className)}>
      <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
      <div className="min-w-0">
        {title && <p className="font-medium">{title}</p>}
        {children && <div className={cn("text-nd-muted", title && "mt-0.5")}>{children}</div>}
      </div>
    </div>
  );
}

export function ComingSoon({ children = "Disponível em breve" }: { children?: ReactNode }) {
  return <span className="inline-flex items-center gap-1 text-xs text-nd-faint"><Clock className="h-3 w-3" aria-hidden />{children}</span>;
}
