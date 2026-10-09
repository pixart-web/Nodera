import { Activity, AppWindow, Bot, Boxes, BrainCircuit, Cloud, Database, FileText, FolderKanban, Globe, KeyRound, LayoutDashboard, LockKeyhole, Rocket, ScrollText, Server, Settings, ShieldCheck, Users, UserCog, Wrench, Briefcase, ListChecks } from "lucide-react";
import type { LucideIcon } from "lucide-react";

export interface NavItem { href: string; label: string; icon: LucideIcon; badge?: string }
export interface NavGroup { label?: string; items: NavItem[] }

export const NAV: NavGroup[] = [
  {
    items: [
      { href: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
      { href: "/projects", label: "Projetos", icon: FolderKanban },
      { href: "/wordpress", label: "Sites WordPress", icon: Boxes },
      { href: "/applications", label: "Aplicações", icon: AppWindow },
      { href: "/clients", label: "Clientes", icon: Briefcase },
      { href: "/domains", label: "Domínios", icon: Globe },
      { href: "/ssl", label: "SSL / Certificados", icon: LockKeyhole },
      { href: "/backups", label: "Backups", icon: Database },
      { href: "/monitoring", label: "Monitorização", icon: Activity },
      { href: "/logs", label: "Logs", icon: ScrollText },
      { href: "/users", label: "Utilizadores", icon: Users },
      { href: "/settings", label: "Definições", icon: Settings },
    ],
  },
  {
    label: "Operações",
    items: [
      { href: "/deployment", label: "Deployment", icon: Rocket },
      { href: "/migration", label: "Migração", icon: Cloud },
    ],
  },
  {
    label: "Control Plane (API)",
    items: [
      { href: "/operations", label: "Visão geral", icon: ListChecks },
      { href: "/infrastructure", label: "Infraestrutura", icon: Server },
      { href: "/jobs", label: "Jobs", icon: Wrench },
      { href: "/tools", label: "Tools & Aprovações", icon: ShieldCheck },
      { href: "/agents", label: "Agentes", icon: Bot },
      { href: "/ai", label: "AI Gateway", icon: BrainCircuit },
      { href: "/secrets", label: "Secrets", icon: KeyRound },
      { href: "/access", label: "Acessos", icon: UserCog },
      { href: "/audit", label: "Auditoria", icon: FileText },
    ],
  },
];

export function flatNav(): NavItem[] {
  return NAV.flatMap((g) => g.items);
}
