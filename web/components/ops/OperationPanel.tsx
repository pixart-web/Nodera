"use client";

import { useEffect, useRef, useState } from "react";
import { CheckCircle2, CircleDashed, Loader2, RotateCcw, XCircle } from "lucide-react";
import { Modal } from "@/components/ui/Overlay";
import { Button } from "@/components/ui/Button";
import { Alert } from "@/components/ui/Feedback";
import { ProgressBar } from "@/components/ui/Charts";
import { api } from "@/lib/api";
import { streamSSE } from "@/lib/stream";
import type { ApiOperation, ApiOperationLog, ApiOperationStep } from "@/lib/types";

const LEVEL: Record<string, string> = { debug: "text-nd-faint", info: "text-nd-info", success: "text-nd-success", warning: "text-nd-warning", error: "text-nd-danger" };

function StepIcon({ status }: { status: string }) {
  if (status === "succeeded") return <CheckCircle2 className="h-4 w-4 text-nd-success" aria-label="concluído" />;
  if (status === "failed") return <XCircle className="h-4 w-4 text-nd-danger" aria-label="falhou" />;
  if (status === "rolled_back" || status === "rolling_back") return <RotateCcw className="h-4 w-4 text-nd-warning" aria-label="revertido" />;
  if (status === "running") return <Loader2 className="h-4 w-4 animate-spin text-nd-info motion-reduce:animate-none" aria-label="em curso" />;
  return <CircleDashed className="h-4 w-4 text-nd-faint" aria-label="pendente" />;
}

// OperationPanel follows one operation live (SSE) and shows the real steps,
// logs, result and — if it failed — what was rolled back. Nothing is
// simulated: every line comes from the persisted operation.
export function OperationPanel({ jobId, title, onClose }: { jobId: string; title: string; onClose: (ok: boolean) => void }) {
  const [op, setOp] = useState<ApiOperation | null>(null);
  const [steps, setSteps] = useState<ApiOperationStep[]>([]);
  const [logs, setLogs] = useState<ApiOperationLog[]>([]);
  const [streamError, setStreamError] = useState<string | null>(null);
  const logRef = useRef<HTMLDivElement>(null);
  const lastLog = useRef<string>("");

  useEffect(() => {
    const ctrl = new AbortController();
    let stopped = false;
    async function run() {
      // Reconnect with Last-Event-ID until the server says the operation ended.
      while (!stopped) {
        let ended = false;
        try {
          await streamSSE(`/api/v1/operations/${jobId}/stream`, (e) => {
            if (e.event === "log") {
              const l = e.data as ApiOperationLog;
              setLogs((cur) => (cur.some((x) => x.id === l.id) ? cur : [...cur, l]));
              lastLog.current = String(l.id);
            } else if (e.event === "status") {
              const d = e.data as { operation: ApiOperation; steps: ApiOperationStep[] };
              setOp(d.operation);
              setSteps(d.steps ?? []);
            } else if (e.event === "end") {
              ended = true;
            }
          }, ctrl.signal, lastLog.current || undefined);
          setStreamError(null);
        } catch (err) {
          if (ctrl.signal.aborted) return;
          setStreamError(err instanceof Error ? err.message : "stream interrompido");
          // fall back to a one-shot read so the user still sees the final state
          try {
            const o = await api.get<ApiOperation>(`/api/v1/operations/${jobId}`);
            setOp(o);
            const s = await api.get<ApiOperationStep[]>(`/api/v1/operations/${jobId}/steps`);
            setSteps(s);
          } catch { /* keep trying */ }
        }
        if (ended) return;
        await new Promise((r) => setTimeout(r, 1500));
      }
    }
    void run();
    return () => { stopped = true; ctrl.abort(); };
  }, [jobId]);

  useEffect(() => { logRef.current?.scrollTo({ top: logRef.current.scrollHeight }); }, [logs]);

  const done = op && ["succeeded", "failed", "cancelled"].includes(op.status);
  const ok = op?.status === "succeeded";

  async function cancel() {
    try { await api.post(`/api/v1/operations/${jobId}/cancel`); } catch { /* shown by status */ }
  }

  return (
    <Modal open onClose={() => onClose(!!ok)} title={title} size="lg"
      description={op ? `Operação ${op.operation} · ${op.status}` : "A iniciar…"}
      footer={<>
        {!done && <Button onClick={cancel} disabled={!op || op.cancel_requested}>{op?.cancel_requested ? "Cancelamento pedido…" : "Cancelar operação"}</Button>}
        <Button variant="primary" onClick={() => onClose(!!ok)}>{done ? "Fechar" : "Continuar em segundo plano"}</Button>
      </>}>
      <div className="space-y-4">
        <ProgressBar label="Progresso" value={op?.progress ?? 0} tone="primary" />
        {streamError && !done && <Alert tone="warning" title="Ligação em tempo real interrompida">A tentar reconectar… ({streamError})</Alert>}
        {done && ok && <Alert tone="success" title="Concluída com sucesso">{op?.result ? <pre className="mt-1 max-h-28 overflow-auto whitespace-pre-wrap text-xs">{JSON.stringify(op.result, null, 2)}</pre> : null}</Alert>}
        {done && !ok && (
          <Alert tone="error" title={op?.status === "cancelled" ? "Operação cancelada" : "A operação falhou"}>
            {op?.error}
            {op?.rolled_back && <p className="mt-1">As alterações já feitas foram revertidas automaticamente. Podes tentar novamente depois de corrigir a causa.</p>}
          </Alert>
        )}
        <ol className="space-y-1.5" aria-label="Passos da operação">
          {steps.map((s) => (
            <li key={s.seq} className="flex items-center gap-2.5 text-sm">
              <StepIcon status={s.status} /><span className="font-mono text-xs text-nd-text">{s.name}</span>
              {s.error && <span className="truncate text-xs text-nd-danger">{s.error}</span>}
            </li>
          ))}
        </ol>
        <div ref={logRef} role="log" aria-label="Registo da operação" tabIndex={0} className="nd-scroll max-h-56 overflow-auto rounded-nd bg-[#050C17] p-3 font-mono text-xs leading-5">
          {logs.length === 0 ? <span className="text-nd-faint">A aguardar registos…</span> : logs.map((l) => (
            <div key={l.id} className="flex gap-2"><span className={`w-14 shrink-0 font-semibold uppercase ${LEVEL[l.level] ?? ""}`}>{l.level}</span><span className="text-nd-text">{l.message}</span></div>
          ))}
        </div>
      </div>
    </Modal>
  );
}
