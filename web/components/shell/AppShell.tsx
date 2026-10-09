"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import { usePathname, useRouter } from "next/navigation";
import { Cloud, Database, FolderKanban, Globe, Plus, ScrollText, Search, Settings, Boxes } from "lucide-react";
import { Sidebar, SidebarContent } from "./Sidebar";
import { Topbar } from "./Topbar";
import { Drawer } from "@/components/ui/Overlay";
import { CommandPalette, type Command } from "@/components/ui/CommandPalette";
import { ToastProvider, useToast } from "@/components/ui/Toast";
import { flatNav } from "./nav";

function Shell({ children }: { children: ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const toast = useToast();
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

  const commands = useMemo<Command[]>(() => {
    const notConnected = (what: string) => () => toast.push("info", `${what}: operação ainda não ligada ao backend.`);
    return [
      { id: "new-project", group: "Ações", label: "Novo projeto", icon: <Plus />, run: () => router.push("/deployment") },
      { id: "new-wp", group: "Ações", label: "Novo WordPress", icon: <Boxes />, run: notConnected("Novo WordPress") },
      { id: "migrate", group: "Ações", label: "Migrar WordPress", icon: <Cloud />, run: () => router.push("/migration") },
      { id: "add-domain", group: "Ações", label: "Adicionar domínio", icon: <Globe />, run: () => router.push("/domains") },
      { id: "backup", group: "Ações", label: "Criar backup", icon: <Database />, run: () => router.push("/backups") },
      { id: "logs", group: "Ações", label: "Ver logs", icon: <ScrollText />, run: () => router.push("/logs") },
      { id: "search-project", group: "Ações", label: "Pesquisar projeto", icon: <Search />, run: () => router.push("/projects") },
      { id: "settings", group: "Ações", label: "Abrir definições", icon: <Settings />, run: () => router.push("/settings") },
      ...flatNav().map((n) => ({ id: `go-${n.href}`, group: "Ir para", label: n.label, icon: <n.icon />, run: () => router.push(n.href) })),
    ];
  }, [router, toast]);

  return (
    <div className="min-h-screen bg-nd-bg">
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-[100] focus:rounded-nd focus:bg-nd-primary focus:px-3 focus:py-2 focus:text-white">Saltar para o conteúdo</a>
      <Sidebar />
      <Drawer open={menu} onClose={() => setMenu(false)} title="Menu de navegação"><SidebarContent onNavigate={() => setMenu(false)} /></Drawer>
      <div className="lg:pl-[var(--sidebar-w)]">
        <Topbar onMenu={() => setMenu(true)} onSearch={() => setPalette(true)} />
        <main id="main" tabIndex={-1} className="mx-auto w-full max-w-[1480px] px-4 py-6 md:px-6 md:py-8">
          <div key={pathname} className="nd-fade-in">{children}</div>
        </main>
      </div>
      <CommandPalette open={palette} onClose={() => setPalette(false)} commands={commands} />
    </div>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  return <ToastProvider><Shell>{children}</Shell></ToastProvider>;
}
