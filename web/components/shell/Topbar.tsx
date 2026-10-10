"use client";

import { ChevronDown, LogOut, Menu, Search, User as UserIcon, Settings } from "lucide-react";
import { useRouter } from "next/navigation";
import { Avatar } from "@/components/ui/Avatar";
import { Dropdown } from "@/components/ui/Dropdown";
import { IconButton } from "@/components/ui/Button";
import { Logo } from "./Logo";
import { NotificationBell } from "./NotificationBell";
import { clearSession, getStoredUser } from "@/lib/session";
import { api } from "@/lib/api";

export function Topbar({ onMenu, onSearch }: { onMenu: () => void; onSearch: () => void }) {
  const router = useRouter();
  const user = getStoredUser();
  const name = user?.display_name || "Utilizador";
  const initials = name.split(" ").map((p) => p[0]).slice(0, 2).join("").toUpperCase();

  async function logout() {
    try { await api.post("/api/v1/auth/logout", undefined, false); } catch { /* best-effort */ }
    clearSession();
    router.replace("/login");
  }

  return (
    <header className="sticky top-0 z-20 flex h-[var(--topbar-h)] items-center gap-3 border-b border-nd-border bg-nd-bg/85 px-4 backdrop-blur md:px-6">
      <IconButton label="Abrir menu" onClick={onMenu} className="lg:hidden"><Menu className="h-5 w-5" /></IconButton>
      <span className="lg:hidden"><Logo className="[&_span]:text-lg" /></span>

      <button type="button" onClick={onSearch} aria-label="Pesquisar (Ctrl K)"
        className="group hidden h-10 max-w-xl flex-1 items-center gap-3 rounded-nd border border-nd-border bg-nd-surface px-3.5 text-left text-sm text-nd-faint transition-colors duration-150 hover:border-nd-strong md:flex">
        <Search className="h-4 w-4 text-nd-muted" aria-hidden />
        <span className="flex-1 truncate">Pesquisar projetos, domínios, containers…</span>
        <kbd className="rounded border border-nd-strong px-1.5 py-0.5 text-[11px] text-nd-muted">⌘K</kbd>
      </button>

      <div className="ml-auto flex items-center gap-1.5">
        <IconButton label="Pesquisar" onClick={onSearch} className="md:hidden"><Search className="h-5 w-5" /></IconButton>
        <NotificationBell />
        <Dropdown label="Menu do utilizador" items={[
          { label: "A minha conta", icon: <UserIcon />, onSelect: () => router.push("/account") },
          { label: "Definições", icon: <Settings />, onSelect: () => router.push("/settings") },
          { label: "Trocar organização", icon: <Menu />, onSelect: () => router.push("/orgs") },
          { label: "Terminar sessão", icon: <LogOut />, danger: true, separatorBefore: true, onSelect: logout },
        ]} trigger={
          <span className="ml-1 flex items-center gap-3 rounded-nd px-2 py-1 transition-colors hover:bg-nd-hover">
            <Avatar initial={initials || "U"} color="#334155" round size={36} />
            <span className="hidden text-left leading-tight sm:block">
              <span className="block text-sm font-medium text-nd-text">{name}</span>
              <span className="block text-xs text-nd-muted">Administrador</span>
            </span>
            <ChevronDown className="hidden h-4 w-4 text-nd-muted sm:block" aria-hidden />
          </span>
        } />
      </div>
    </header>
  );
}
