"use client";

import { useEffect, useMemo, useState } from "react";
import { Pause, Play, Trash2, ScrollText } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Card } from "@/components/ui/Card";
import { PageHeader } from "@/components/ui/PageHeader";
import { SearchInput, Select } from "@/components/ui/Forms";
import { EmptyState, ErrorState, LoadingState, NotConnected } from "@/components/ui/Feedback";
import { useAsync } from "@/lib/useAsync";
import { logsService } from "@/services";
import type { LogLevel, LogLine } from "@/lib/domain";

const LEVEL_CLS: Record<LogLevel, string> = {
  INFO: "text-nd-info", SUCCESS: "text-nd-success", WARNING: "text-nd-warning", ERROR: "text-nd-danger", DEBUG: "text-nd-faint",
};

export default function LogsPage() {
  const { data, loading, error, reload } = useAsync(() => logsService.list());
  const [lines, setLines] = useState<LogLine[]>([]);
  const [live, setLive] = useState(false);
  const [q, setQ] = useState(""); const [level, setLevel] = useState(""); const [project, setProject] = useState("");

  useEffect(() => { if (data) setLines(data); }, [data]);
  // Live mode only replays the demo sample — it does not stream real logs.
  useEffect(() => {
    if (!live || !data) return;
    let i = 0;
    const t = setInterval(() => { const s = data[i++ % data.length]!; setLines((l) => [{ ...s, id: `live-${Date.now()}` }, ...l].slice(0, 300)); }, 1200);
    return () => clearInterval(t);
  }, [live, data]);

  const shown = useMemo(() => lines.filter((l) => (!level || l.level === level) && (!project || l.project === project) && (!q || `${l.message} ${l.source}`.toLowerCase().includes(q.toLowerCase()))), [lines, q, level, project]);
  const projects = useMemo(() => Array.from(new Set(lines.map((l) => l.project))), [lines]);

  return (
    <div>
      <PageHeader title="Logs" description="Consola de logs da infraestrutura." />
      <NotConnected what="As linhas de log são dados de demonstração; o modo live apenas repete uma amostra." />
      <Card className="mt-5 overflow-hidden">
        <div className="grid gap-3 border-b border-nd-border p-4 md:grid-cols-[1fr_150px_170px_auto]">
          <SearchInput aria-label="Pesquisar nos logs" placeholder="Pesquisar mensagem ou origem…" value={q} onChange={(e) => setQ(e.target.value)} />
          <Select aria-label="Nível" value={level} onChange={(e) => setLevel(e.target.value)}><option value="">Todos os níveis</option>{(Object.keys(LEVEL_CLS) as LogLevel[]).map((l) => <option key={l}>{l}</option>)}</Select>
          <Select aria-label="Projeto" value={project} onChange={(e) => setProject(e.target.value)}><option value="">Todos os projetos</option>{projects.map((p) => <option key={p}>{p}</option>)}</Select>
          <div className="flex gap-2">
            <Button onClick={() => setLive((v) => !v)} aria-pressed={live} icon={live ? <Pause className="h-4 w-4" /> : <Play className="h-4 w-4" />}>{live ? "Pausar" : "Live"}</Button>
            <Button onClick={() => setLines([])} icon={<Trash2 className="h-4 w-4" />}>Limpar</Button>
          </div>
        </div>
        <div className="nd-scroll max-h-[60vh] overflow-auto bg-[#050C17] p-4 font-mono text-xs leading-6" role="log" aria-live="off" tabIndex={0} aria-label="Linhas de log">
          {loading ? <LoadingState /> : error ? <ErrorState message={error} onRetry={reload} /> : shown.length === 0
            ? <EmptyState icon={<ScrollText />} title="Sem linhas de log" description="Ajusta os filtros ou limpa a pesquisa." />
            : shown.map((l) => (
              <div key={l.id} className="flex gap-3 whitespace-nowrap hover:bg-white/[0.03]">
                <span className="text-nd-faint">[{l.ts}]</span>
                <span className={`w-16 font-semibold ${LEVEL_CLS[l.level]}`}>{l.level}</span>
                <span className="w-16 text-nd-muted">{l.source}</span>
                <span className="w-32 truncate text-nd-primary-soft">{l.project}</span>
                <span className="text-nd-text">{l.message}</span>
              </div>))}
        </div>
      </Card>
    </div>
  );
}
