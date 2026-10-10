"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import { usePathname, useRouter } from "next/navigation";
import { Cloud, Database, Globe, Plus, ScrollText, Search, Settings, Boxes } from "lucide-react";
import { Sidebar, SidebarContent } from "./Sidebar";
import { Topbar } from "./Topbar";
import { Drawer } from "@/components/ui/Overlay";
import { CommandPalette, type Command } from "@/components/ui/CommandPalette";
import { ToastProvider } from "@/components/ui/Toast";
import { PlatformProvider, usePlatform } from "@/components/providers/Platform";
import { OperationsProvider } from "@/components/providers/Operations";
import { DemoBanner } from "@/components/ui/DemoBanner";
import { api } from "@/lib/api";
import type { ApiSearchHit } from "@/lib/types";
import { flatNav } from "./nav";

function Shell({ children }: { children: ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const [menu, setMenu] = useState(false);
  const [palette, setPalette] = useState(false);

  useEffect(() => { setMenu(false); }, [pathname]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setPalette((p) => !p); }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const { can } = usePlatform();
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<ApiSearchHit[]>([]);
  useEffect(() => {
    if (!palette || query.trim().length < 2) { setHits([]); return; }
    const t = setTimeout(() => {
      api.get<{ hits: ApiSearchHit[] }>(`/api/v1/search?q=${encodeURIComponent(query.trim())}`).then((r) => setHits(r.hits)).catch(() => setHits([]));
    }, 200);
    return () => clearTimeout(t);
  }, [query, palette]);

  const commands = useMemo<Command[]>(() => {
    // Only commands the user may actually run are offered; the API still
    // authorises each request, so this is about not offering dead ends.
    const actions: Array<Command & { perm?: string }> = [
      { id: "new-project", group: "Ações", label: "Novo projeto", icon: <Plus />, perm: "projects.create", run: () => router.push("/projects?new=1") },
      { id: "new-wp", group: "Ações", label: "Novo WordPress", icon: <Boxes />, perm: "projects.create", run: () => router.push("/projects?new=wordpress") },
      { id: "migrate", group: "Ações", label: "Migrar WordPress", icon: <Cloud />, perm: "migrations.create", run: () => router.push("/migration") },
      { id: "deploy", group: "Ações", label: "Fazer deployment", icon: <Plus />, perm: "deployments.create", run: () => router.push("/deployment") },
      { id: "add-domain", group: "Ações", label: "Adicionar domínio", icon: <Globe />, perm: "domains.manage", run: () => router.push("/domains?new=1") },
      { id: "backup", group: "Ações", label: "Criar backup", icon: <Database />, perm: "backups.manage", run: () => router.push("/backups?new=1") },
      { id: "logs", group: "Ações", label: "Ver logs", icon: <ScrollText />, perm: "logs.read", run: () => router.push("/logs") },
      { id: "settings", group: "Ações", label: "Abrir definições", icon: <Settings />, run: () => router.push("/settings") },
    ];
    const allowed = actions.filter((a) => !a.perm || can(a.perm));
    const found: Command[] = hits.map((h) => ({ id: `hit-${h.type}-${h.id}`, group: "Resultados", label: `${h.title}${h.hint ? " — " + h.hint : ""}`, keywords: query, icon: <Search />, run: () => router.push(h.path) }));
    return [
      ...found,
      ...allowed,
      ...flatNav().map((n) => ({ id: `go-${n.href}`, group: "Ir para", label: n.label, icon: <n.icon />, run: () => router.push(n.href) })),
    ];
  }, [router, can, hits, query]);

  return (
    <div className="min-h-screen bg-nd-bg">
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-[100] focus:rounded-nd focus:bg-nd-primary focus:px-3 focus:py-2 focus:text-white">Saltar para o conteúdo</a>
      <Sidebar />
      <Drawer open={menu} onClose={() => setMenu(false)} title="Menu de navegação"><SidebarContent onNavigate={() => setMenu(false)} /></Drawer>
      <div className="lg:pl-[var(--sidebar-w)]">
        <Topbar onMenu={() => setMenu(true)} onSearch={() => setPalette(true)} />
        <main id="main" tabIndex={-1} className="mx-auto w-full max-w-[1480px] px-4 py-6 md:px-6 md:py-8">
          <DemoBanner />
          <div key={pathname} className="nd-fade-in">{children}</div>
        </main>
      </div>
      <CommandPalette open={palette} onClose={() => setPalette(false)} commands={commands} onQueryChange={setQuery} />
    </div>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  return <ToastProvider><PlatformProvider><OperationsProvider><Shell>{children}</Shell></OperationsProvider></PlatformProvider></ToastProvider>;
}
