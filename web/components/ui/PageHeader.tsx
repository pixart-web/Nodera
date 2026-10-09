import type { ReactNode } from "react";

export function PageHeader({ title, description, actions, meta }: { title: ReactNode; description?: string; actions?: ReactNode; meta?: ReactNode }) {
  return (
    <header className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold tracking-tight text-nd-text sm:text-[28px]">{title}</h1>
        {description && <p className="mt-1.5 text-sm text-nd-muted sm:text-base">{description}</p>}
        {meta && <div className="mt-2">{meta}</div>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  );
}
