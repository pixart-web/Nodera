"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { CornerDownLeft, Search } from "lucide-react";
import { cn } from "./cn";

export interface Command { id: string; label: string; group: string; icon?: ReactNode; keywords?: string; run: () => void }

export function CommandPalette({ open, onClose, commands }: { open: boolean; onClose: () => void; commands: Command[] }) {
  const [q, setQ] = useState("");
  const [idx, setIdx] = useState(0);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => { if (open) { setQ(""); setIdx(0); setTimeout(() => input.current?.focus(), 0); } }, [open]);

  const results = useMemo(() => {
    const n = q.trim().toLowerCase();
    return n ? commands.filter((c) => `${c.label} ${c.keywords ?? ""} ${c.group}`.toLowerCase().includes(n)) : commands;
  }, [q, commands]);

  useEffect(() => { setIdx(0); }, [q]);

  if (!open || typeof document === "undefined") return null;

  function onKey(e: React.KeyboardEvent) {
    if (e.key === "Escape") onClose();
    else if (e.key === "ArrowDown") { e.preventDefault(); setIdx((i) => Math.min(i + 1, results.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setIdx((i) => Math.max(i - 1, 0)); }
    else if (e.key === "Enter") { const c = results[idx]; if (c) { onClose(); c.run(); } }
  }

  let lastGroup = "";
  return createPortal(
    <div className="fixed inset-0 z-[85] flex items-start justify-center p-4 pt-[12vh]" onKeyDown={onKey}>
      <div className="absolute inset-0 bg-black/60 backdrop-blur-[2px] nd-fade-in" onClick={onClose} aria-hidden />
      <div role="dialog" aria-modal="true" aria-label="Paleta de comandos" className="nd-pop-in relative w-full max-w-xl overflow-hidden rounded-nd-lg border border-nd-strong bg-nd-surface shadow-pop">
        <div className="flex items-center gap-3 border-b border-nd-border px-4">
          <Search className="h-4 w-4 text-nd-muted" aria-hidden />
          <input ref={input} value={q} onChange={(e) => setQ(e.target.value)} role="combobox" aria-expanded aria-controls="cmd-list" aria-activedescendant={results[idx] ? `cmd-${results[idx]!.id}` : undefined}
            placeholder="Escreve um comando ou pesquisa…" className="h-12 flex-1 bg-transparent text-sm text-nd-text placeholder:text-nd-faint focus:outline-none" />
          <kbd className="rounded border border-nd-strong px-1.5 py-0.5 text-[11px] text-nd-muted">Esc</kbd>
        </div>
        <ul id="cmd-list" role="listbox" className="nd-scroll max-h-[50vh] overflow-y-auto p-2">
          {results.length === 0 && <li className="px-3 py-8 text-center text-sm text-nd-muted">Sem resultados para “{q}”.</li>}
          {results.map((c, i) => {
            const header = c.group !== lastGroup ? c.group : null;
            lastGroup = c.group;
            return (
              <li key={c.id} role="presentation">
                {header && <p className="px-3 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-wider text-nd-faint">{header}</p>}
                <div id={`cmd-${c.id}`} role="option" aria-selected={i === idx} onMouseEnter={() => setIdx(i)} onClick={() => { onClose(); c.run(); }}
                  className={cn("flex cursor-pointer items-center gap-3 rounded-md px-3 py-2 text-sm text-nd-text", i === idx && "bg-nd-hover")}>
                  <span className="text-nd-muted [&>svg]:h-4 [&>svg]:w-4">{c.icon}</span>
                  <span className="flex-1">{c.label}</span>
                  {i === idx && <CornerDownLeft className="h-3.5 w-3.5 text-nd-faint" aria-hidden />}
                </div>
              </li>
            );
          })}
        </ul>
      </div>
    </div>,
    document.body,
  );
}
