"use client";

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { cn } from "./cn";

export interface MenuItem { label: string; icon?: ReactNode; onSelect?: () => void; danger?: boolean; disabled?: boolean; separatorBefore?: boolean }

export function Dropdown({ trigger, items, align = "right", label }: {
  trigger: ReactNode; items: MenuItem[]; align?: "left" | "right"; label: string;
}) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const id = useId();

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => { if (!root.current?.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  function onKey(e: React.KeyboardEvent) {
    if (e.key === "Escape") { setOpen(false); return; }
    if (!open && (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ")) { e.preventDefault(); setOpen(true); setActive(0); return; }
    if (!open) return;
    if (e.key === "ArrowDown") { e.preventDefault(); setActive((a) => (a + 1) % items.length); }
    if (e.key === "ArrowUp") { e.preventDefault(); setActive((a) => (a - 1 + items.length) % items.length); }
    if (e.key === "Enter") { e.preventDefault(); const it = items[active]; if (it && !it.disabled) { it.onSelect?.(); setOpen(false); } }
  }

  return (
    <div ref={root} className="relative" onKeyDown={onKey}>
      <button type="button" aria-haspopup="menu" aria-expanded={open} aria-controls={id} aria-label={label}
        onClick={() => { setOpen((o) => !o); setActive(0); }} className="rounded-nd">
        {trigger}
      </button>
      {open && (
        <div id={id} role="menu" aria-label={label}
          className={cn("nd-pop-in absolute z-50 mt-2 min-w-[200px] rounded-nd-lg border border-nd-strong bg-nd-surface p-1.5 shadow-pop", align === "right" ? "right-0" : "left-0")}>
          {items.map((it, i) => (
            <div key={it.label}>
              {it.separatorBefore && <div className="my-1 h-px bg-nd-border" role="separator" />}
              <button type="button" role="menuitem" disabled={it.disabled} onMouseEnter={() => setActive(i)}
                onClick={() => { it.onSelect?.(); setOpen(false); }}
                className={cn("flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-sm transition-colors disabled:opacity-40",
                  it.danger ? "text-nd-danger" : "text-nd-text", i === active && "bg-nd-hover")}>
                {it.icon && <span className="[&>svg]:h-4 [&>svg]:w-4 text-nd-muted">{it.icon}</span>}
                {it.label}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function Tooltip({ text, children }: { text: string; children: ReactNode }) {
  return (
    <span className="group relative inline-flex">
      {children}
      <span role="tooltip" className="pointer-events-none absolute bottom-full left-1/2 z-50 mb-2 -translate-x-1/2 whitespace-nowrap rounded-md border border-nd-strong bg-nd-raised px-2 py-1 text-xs text-nd-text opacity-0 shadow-pop transition-opacity duration-150 group-focus-within:opacity-100 group-hover:opacity-100">
        {text}
      </span>
    </span>
  );
}
