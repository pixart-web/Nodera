"use client";

import { useState, type ReactNode } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "./Button";
import { EmptyState, ErrorState, LoadingState } from "./Feedback";
import { cn } from "./cn";

export interface Column<T> {
  key: string; header: string; cell: (row: T) => ReactNode; className?: string;
  mobileLabel?: string; hideOnMobile?: boolean; primary?: boolean;
}

// Table on >=md, stacked cards below — not a squeezed desktop table.
export function DataTable<T extends { id: string }>({ columns, rows, loading, error, onRetry, empty, pageSize = 8, caption }: {
  columns: Column<T>[]; rows: T[] | null; loading?: boolean; error?: string | null; onRetry?: () => void;
  empty: { title: string; description?: string; action?: { label: string; onClick?: () => void }; icon?: ReactNode };
  pageSize?: number; caption: string;
}) {
  const [page, setPage] = useState(0);
  if (loading) return <LoadingState rows={4} />;
  if (error) return <ErrorState message={error} onRetry={onRetry} />;
  if (!rows || rows.length === 0) return <EmptyState {...empty} />;

  const pages = Math.ceil(rows.length / pageSize);
  const cur = Math.min(page, pages - 1);
  const slice = rows.slice(cur * pageSize, cur * pageSize + pageSize);

  return (
    <div>
      <div className="hidden overflow-x-auto md:block nd-scroll">
        <table className="w-full text-sm">
          <caption className="sr-only">{caption}</caption>
          <thead>
            <tr className="border-b border-nd-border">
              {columns.map((c) => <th key={c.key} scope="col" className={cn("px-3 py-2.5 text-left text-xs font-medium uppercase tracking-wide text-nd-faint", c.className)}>{c.header}</th>)}
            </tr>
          </thead>
          <tbody>
            {slice.map((r) => (
              <tr key={r.id} className="border-b border-nd-border/60 transition-colors duration-150 last:border-0 hover:bg-nd-hover/60">
                {columns.map((c) => <td key={c.key} className={cn("px-3 py-3 align-middle text-nd-text", c.className)}>{c.cell(r)}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ul className="space-y-3 md:hidden" aria-label={caption}>
        {slice.map((r) => (
          <li key={r.id} className="rounded-nd border border-nd-border bg-nd-elevated p-3.5">
            {columns.filter((c) => c.primary).map((c) => <div key={c.key} className="mb-2">{c.cell(r)}</div>)}
            <dl className="grid grid-cols-2 gap-x-3 gap-y-2 text-sm">
              {columns.filter((c) => !c.primary && !c.hideOnMobile && c.header).map((c) => (
                <div key={c.key} className="min-w-0">
                  <dt className="text-[11px] uppercase tracking-wide text-nd-faint">{c.mobileLabel ?? c.header}</dt>
                  <dd className="truncate text-nd-text">{c.cell(r)}</dd>
                </div>
              ))}
            </dl>
          </li>
        ))}
      </ul>
      {pages > 1 && (
        <div className="mt-3 flex items-center justify-between border-t border-nd-border pt-3 text-xs text-nd-muted">
          <span>{cur * pageSize + 1}–{Math.min((cur + 1) * pageSize, rows.length)} de {rows.length}</span>
          <div className="flex items-center gap-1">
            <Button size="sm" variant="ghost" disabled={cur === 0} onClick={() => setPage(cur - 1)} aria-label="Página anterior"><ChevronLeft className="h-4 w-4" /></Button>
            <span aria-live="polite">{cur + 1} / {pages}</span>
            <Button size="sm" variant="ghost" disabled={cur >= pages - 1} onClick={() => setPage(cur + 1)} aria-label="Página seguinte"><ChevronRight className="h-4 w-4" /></Button>
          </div>
        </div>
      )}
    </div>
  );
}
