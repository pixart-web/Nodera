import type { ReactNode } from "react";

// Legacy-compatible page header (title/description) restyled on the Nodera
// tokens. `actions` renders the primary buttons on the right.
export function PageHeader({ title, description, actions }: { title: string; description?: string; actions?: ReactNode }) {
  return (
    <header className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold tracking-tight text-nd-text sm:text-[28px]">{title}</h1>
        {description && <p className="mt-1.5 text-sm text-nd-muted sm:text-base">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  );
}
