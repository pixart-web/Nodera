"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { BookOpen } from "lucide-react";
import { NAV } from "./nav";
import { Logo } from "./Logo";
import { cn } from "@/components/ui/cn";

export function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  return (
    <div className="flex h-full flex-col">
      <div className="flex h-16 shrink-0 items-center px-5"><Link href="/dashboard" aria-label="Nodera — Dashboard" onClick={onNavigate}><Logo /></Link></div>
      <nav aria-label="Navegação principal" className="nd-scroll flex-1 space-y-5 overflow-y-auto px-3 pb-4 pt-2">
        {NAV.map((group, gi) => (
          <div key={gi}>
            {group.label && <p className="mb-1.5 px-3 text-[11px] font-semibold uppercase tracking-wider text-nd-faint">{group.label}</p>}
            <ul className="space-y-0.5">
              {group.items.map((it) => {
                const active = pathname === it.href || pathname.startsWith(it.href + "/");
                const Icon = it.icon;
                return (
                  <li key={it.href}>
                    <Link href={it.href} onClick={onNavigate} aria-current={active ? "page" : undefined}
                      className={cn("group relative flex items-center gap-3 rounded-nd px-3 py-2 text-sm font-medium transition-colors duration-150",
                        active ? "bg-nd-primary/15 text-nd-text" : "text-nd-muted hover:bg-nd-hover hover:text-nd-text")}>
                      {active && <span aria-hidden className="absolute -left-3 top-1.5 h-[calc(100%-12px)] w-[3px] rounded-r-full bg-nd-primary" />}
                      <Icon className={cn("h-[18px] w-[18px] shrink-0", active ? "text-nd-primary-soft" : "text-nd-faint group-hover:text-nd-muted")} aria-hidden />
                      <span className="truncate">{it.label}</span>
                      {it.badge && <span className="ml-auto rounded-full bg-nd-primary/20 px-1.5 text-[11px] text-nd-primary-soft">{it.badge}</span>}
                    </Link>
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </nav>
      <div className="m-3 shrink-0 rounded-nd-lg border border-nd-border bg-nd-surface p-3">
        <div className="flex items-center gap-2.5">
          <svg width="22" height="22" viewBox="0 0 32 32" aria-hidden><path d="M5 27V5h5.2l11.6 14.6V5H27v22h-5.2L10.2 12.4V27z" fill="#38BDF8" /></svg>
          <div className="leading-tight"><p className="text-sm font-medium text-nd-text">Nodera v1.0</p><p className="text-xs text-nd-muted">Infra Manager</p></div>
        </div>
        <Link href="/settings" className="mt-3 flex items-center gap-2 rounded-md bg-nd-primary/10 px-2.5 py-1.5 text-xs font-medium text-nd-primary-soft transition-colors hover:bg-nd-primary/20">
          <BookOpen className="h-3.5 w-3.5" aria-hidden />Ver documentação
        </Link>
      </div>
    </div>
  );
}

export function Sidebar() {
  return (
    <aside className="fixed inset-y-0 left-0 z-30 hidden w-[var(--sidebar-w)] border-r border-nd-border bg-nd-elevated lg:block">
      <SidebarContent />
    </aside>
  );
}
