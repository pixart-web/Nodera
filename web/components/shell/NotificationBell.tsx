"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Bell } from "lucide-react";
import { IconButton } from "@/components/ui/Button";
import { notificationsService } from "@/services";
import { streamSSE } from "@/lib/stream";
import { ago } from "@/lib/format";
import type { ApiNotification } from "@/lib/types";

// The unread badge is driven by the organisation event stream (SSE) with a
// plain poll as fallback, so it stays correct even if streaming is blocked.
export function NotificationBell() {
  const [unread, setUnread] = useState(0);
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<ApiNotification[]>([]);
  const box = useRef<HTMLDivElement>(null);

  const refreshCount = useCallback(() => { notificationsService.unread().then(setUnread).catch(() => {}); }, []);
  useEffect(() => {
    refreshCount();
    const poll = setInterval(refreshCount, 60_000);
    const ctrl = new AbortController();
    (async () => {
      while (!ctrl.signal.aborted) {
        try { await streamSSE("/api/v1/events", (e) => { if (e.event === "notifications") setUnread((e.data as { unread: number }).unread); }, ctrl.signal); } catch { /* reconnect */ }
        await new Promise((r) => setTimeout(r, 5000));
      }
    })();
    return () => { clearInterval(poll); ctrl.abort(); };
  }, [refreshCount]);

  useEffect(() => {
    if (!open) return;
    notificationsService.list().then(setItems).catch(() => setItems([]));
    const close = (e: MouseEvent) => { if (!box.current?.contains(e.target as Node)) setOpen(false); };
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", close); document.addEventListener("keydown", esc);
    return () => { document.removeEventListener("mousedown", close); document.removeEventListener("keydown", esc); };
  }, [open]);

  async function markAll() { await notificationsService.markAll().catch(() => {}); setItems((l) => l.map((n) => ({ ...n, read: true }))); setUnread(0); }

  return (
    <div className="relative" ref={box}>
      <IconButton label={unread ? `Notificações (${unread} por ler)` : "Notificações"} aria-expanded={open} onClick={() => setOpen((o) => !o)}><Bell className="h-5 w-5" /></IconButton>
      {unread > 0 && <span aria-hidden className="pointer-events-none absolute right-1 top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-nd-danger px-1 text-[10px] font-semibold text-white ring-2 ring-nd-bg">{unread > 9 ? "9+" : unread}</span>}
      {open && (
        <div role="dialog" aria-label="Notificações" className="nd-pop-in absolute right-0 top-11 z-50 w-[min(92vw,380px)] overflow-hidden rounded-nd-lg border border-nd-strong bg-nd-surface shadow-pop">
          <div className="flex items-center justify-between border-b border-nd-border px-4 py-3"><p className="text-sm font-semibold">Notificações</p>{unread > 0 && <button type="button" className="text-xs text-nd-primary-soft hover:underline" onClick={markAll}>Marcar tudo como lido</button>}</div>
          <ul className="nd-scroll max-h-80 divide-y divide-nd-border/50 overflow-y-auto">
            {items.length === 0 && <li className="px-4 py-8 text-center text-sm text-nd-muted">Sem notificações.</li>}
            {items.map((n) => (
              <li key={n.id} className={`px-4 py-3 text-sm ${n.read ? "opacity-70" : ""}`}>
                <p className="font-medium text-nd-text">{n.title}</p>{n.body && <p className="text-xs text-nd-muted">{n.body}</p>}<p className="mt-1 text-[11px] text-nd-faint">{ago(n.created_at)}</p>
              </li>))}
          </ul>
          <Link href="/notifications" className="block border-t border-nd-border px-4 py-2.5 text-center text-xs text-nd-primary-soft hover:bg-nd-hover" onClick={() => setOpen(false)}>Ver todas</Link>
        </div>)}
    </div>
  );
}
