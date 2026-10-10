"use client";

import Link from "next/link";
import { Code2, ExternalLink, Boxes } from "lucide-react";
import type { Project, ResourceStatus } from "@/lib/domain";
import { Avatar } from "@/components/ui/Avatar";
import { Badge, StatusText } from "@/components/ui/Status";
import { Button } from "@/components/ui/Button";

export function ProjectStatus({ status, label }: { status: ResourceStatus; label?: string }) {
  return <StatusText status={status} label={label} />;
}

export function ProjectTypeBadge({ type }: { type: Project["type"] }) {
  return type === "WORDPRESS"
    ? <Badge><Boxes className="h-3 w-3" aria-hidden />WordPress</Badge>
    : <Badge><Code2 className="h-3 w-3" aria-hidden />Aplicação</Badge>;
}

export function ProjectIdentity({ project }: { project: Project }) {
  return (
    <div className="flex min-w-0 items-center gap-3">
      <Avatar initial={project.initial} color={project.color} size={40} className={project.color === "#FFFFFF" ? "!text-slate-900" : ""} />
      <div className="min-w-0">
        <Link href={`/projects/${project.id}`} className="block truncate text-sm font-semibold text-nd-text hover:text-nd-primary-soft">{project.name}</Link>
        <p className="truncate text-xs text-nd-muted">{project.domain}</p>
      </div>
    </div>
  );
}

export function ProjectRow({ project }: { project: Project }) {
  return (
    <div className="grid grid-cols-[1fr_auto] items-center gap-x-4 gap-y-3 rounded-nd px-3 py-3 transition-colors duration-150 hover:bg-nd-hover/60 md:grid-cols-[minmax(0,1.6fr)_110px_150px_auto]">
      <ProjectIdentity project={project} />
      <div className="hidden md:block"><ProjectTypeBadge type={project.type} /></div>
      <div className="hidden md:block"><ProjectStatus status={project.status} label={project.statusLabel} /></div>
      <div className="col-span-2 flex items-center gap-2 md:col-span-1 md:justify-end">
        <span className="mr-auto flex items-center gap-2 md:hidden"><ProjectStatus status={project.status} label={project.statusLabel} /></span>
        {project.domain !== "—" && <a href={`https://${project.domain}`} target="_blank" rel="noreferrer"><Button size="sm" icon={<ExternalLink className="h-3.5 w-3.5" />}>Abrir</Button></a>}
        <Link href={`/projects/${project.id}`}><Button size="sm">Gerir</Button></Link>
      </div>
    </div>
  );
}
