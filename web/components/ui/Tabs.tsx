"use client";

import { useRef, type ReactNode } from "react";
import { cn } from "./cn";

export interface TabDef { id: string; label: string; icon?: ReactNode }

export function Tabs({ tabs, active, onChange, label }: { tabs: TabDef[]; active: string; onChange: (id: string) => void; label: string }) {
  const refs = useRef<Record<string, HTMLButtonElement | null>>({});
  function onKey(e: React.KeyboardEvent, i: number) {
    let n = i;
    if (e.key === "ArrowRight") n = (i + 1) % tabs.length;
    else if (e.key === "ArrowLeft") n = (i - 1 + tabs.length) % tabs.length;
    else return;
    e.preventDefault();
    const t = tabs[n]!;
    onChange(t.id);
    refs.current[t.id]?.focus();
  }
  return (
    <div role="tablist" aria-label={label} className="nd-scroll -mb-px flex gap-1 overflow-x-auto border-b border-nd-border">
      {tabs.map((t, i) => (
        <button key={t.id} ref={(el) => { refs.current[t.id] = el; }} role="tab" id={`tab-${t.id}`} aria-selected={active === t.id} aria-controls={`panel-${t.id}`}
          tabIndex={active === t.id ? 0 : -1} onClick={() => onChange(t.id)} onKeyDown={(e) => onKey(e, i)}
          className={cn("relative inline-flex items-center gap-2 whitespace-nowrap px-3.5 py-2.5 text-sm font-medium transition-colors duration-150",
            active === t.id ? "text-nd-text after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-full after:bg-nd-primary" : "text-nd-muted hover:text-nd-text")}>
          {t.icon}{t.label}
        </button>
      ))}
    </div>
  );
}

export function TabPanel({ id, active, children }: { id: string; active: string; children: ReactNode }) {
  if (id !== active) return null;
  return <div role="tabpanel" id={`panel-${id}`} aria-labelledby={`tab-${id}`} className="nd-fade-in pt-5">{children}</div>;
}

// Compact segmented control (1h / 6h / 24h / 7d).
export function Segmented<T extends string>({ options, value, onChange, label }: { options: readonly T[]; value: T; onChange: (v: T) => void; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-nd border border-nd-border bg-nd-elevated p-0.5">
      {options.map((o) => (
        <button key={o} type="button" role="radio" aria-checked={value === o} onClick={() => onChange(o)}
          className={cn("rounded-[8px] px-3 py-1 text-xs font-medium transition-colors duration-150", value === o ? "bg-nd-primary text-white" : "text-nd-muted hover:text-nd-text")}>
          {o}
        </button>
      ))}
    </div>
  );
}
