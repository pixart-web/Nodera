"use client";

import { useEffect, useState } from "react";
import { Pause, Play, ScrollText } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Card } from "@/components/ui/Card";
import { PageHeader } from "@/components/ui/PageHeader";
import { SearchInput, Select } from "@/components/ui/Forms";
import { EmptyState, ErrorState, LoadingState } from "@/components/ui/Feedback";
import { useAsync } from "@/lib/useAsync";
import { logsService, projectsService } from "@/services";

const LEVEL_CLS: Record<string, string> = { info: "text-nd-info", success: "text-nd-success", warning: "text-nd-warning", error: "text-nd-danger", debug: "text-nd-faint" };

export default function LogsPage() {
  const [q, setQ] = useState(""); const [level, setLevel] = useState(""); const [project, setProject] = useState(""); const [source, setSource] = useState("");
  const [tail, setTail] = useState(false);
  const projects = useAsync(() => projectsService.list());
  const { data, loading, error, reload } = useAsync(() => logsService.query({ q, level, project_id: project, source }), [q, level, project, source]);
  // "Seguir" polls the stored log every few seconds. Logs are stored lines
  // (redacted); this is not a raw container stream.
  useEffect(() => { if (!tail) return; const t = setInterval(reload, 4000); return () => clearInterval(t); }, [tail, reload]);
  const pname = (id: string | null) => (id ? projects.data?.find((p) => p.id === id)?.name ?? id.slice(0, 8) : "—");

  return (
    <div>
      <PageHeader title="Logs" description="Registos do sistema e das operações, com credenciais mascaradas." />
      <Card className="mt-5 overflow-hidden">
        <div className="grid gap-3 border-b border-nd-border p-4 md:grid-cols-[1fr_130px_130px_170px_auto]">
          <SearchInput aria-label="Pesquisar nos logs" placeholder="Pesquisar mensagem…" value={q} onChange={(e) => setQ(e.target.value)} />
          <Select aria-label="Nível" value={level} onChange={(e) => setLevel(e.target.value)}><option value="">Todos os níveis</option>{Object.keys(LEVEL_CLS).map((l) => <option key={l}>{l}</option>)}</Select>
          <Select aria-label="Origem" value={source} onChange={(e) => setSource(e.target.value)}><option value="">Todas as origens</option>{["application", "container", "deployment", "migration", "system", "audit"].map((l) => <option key={l}>{l}</option>)}</Select>
          <Select aria-label="Projeto" value={project} onChange={(e) => setProject(e.target.value)}><option value="">Todos os projetos</option>{(projects.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</Select>
          <Button onClick={() => setTail((v) => !v)} aria-pressed={tail} icon={tail ? <Pause className="h-4 w-4" /> : <Play className="h-4 w-4" />}>{tail ? "A seguir (4 s)" : "Seguir"}</Button>
        </div>
        <div className="nd-scroll max-h-[60vh] overflow-auto bg-[#050C17] p-4 font-mono text-xs leading-6" role="log" aria-live="off" tabIndex={0} aria-label="Linhas de log">
          {loading && !data ? <LoadingState /> : error ? <ErrorState message={error} onRetry={reload} /> : (data ?? []).length === 0
            ? <EmptyState icon={<ScrollText />} title="Sem linhas de log" description="Ainda não há registos para estes filtros." />
            : (data ?? []).map((l) => (
              <div key={l.id} className="flex gap-3 whitespace-nowrap hover:bg-white/[0.03]">
                <span className="text-nd-faint">[{new Date(l.at).toLocaleTimeString("pt-PT")}]</span>
                <span className={`w-16 font-semibold uppercase ${LEVEL_CLS[l.level] ?? ""}`}>{l.level}</span>
                <span className="w-24 text-nd-muted">{l.source}</span>
                <span className="w-32 truncate text-nd-primary-soft">{pname(l.project_id)}</span>
                <span className="text-nd-text">{l.message}</span>
              </div>))}
        </div>
      </Card>
    </div>
  );
}
