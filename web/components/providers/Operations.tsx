"use client";

import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import Link from "next/link";
import { ApiError } from "@/lib/api";
import { useToast } from "@/components/ui/Toast";
import { OperationPanel } from "@/components/ops/OperationPanel";
import type { ApiGatewayResult, ApiJobRef } from "@/lib/types";

type Outcome = ApiJobRef | ApiGatewayResult;

interface OpsApi {
  // track opens the live progress panel for an already-queued operation.
  track: (jobId: string, title: string, onDone?: (ok: boolean) => void) => void;
  // submit runs a request that queues an operation (or asks for approval) and
  // reports the outcome honestly: queued -> live panel, approval -> notice.
  submit: (title: string, call: () => Promise<Outcome>, onDone?: (ok: boolean) => void) => Promise<boolean>;
}

const Ctx = createContext<OpsApi>({ track: () => {}, submit: async () => false });
export const useOperations = () => useContext(Ctx);

interface Tracked { jobId: string; title: string; onDone?: (ok: boolean) => void }

export function OperationsProvider({ children }: { children: ReactNode }) {
  const toast = useToast();
  const [active, setActive] = useState<Tracked | null>(null);
  const track = useCallback((jobId: string, title: string, onDone?: (ok: boolean) => void) => setActive({ jobId, title, onDone }), []);
  const submit = useCallback(async (title: string, call: () => Promise<Outcome>, onDone?: (ok: boolean) => void) => {
    try {
      const res = await call();
      if ("job_id" in res && res.job_id) {
        track(res.job_id, title, onDone);
        return true;
      }
      const gw = res as ApiGatewayResult;
      if (gw.status === "approval_required") {
        toast.push("info", `“${title}” precisa de aprovação. Nada foi executado — decide em Tools & Aprovações.`);
        return true;
      }
      toast.push("success", `${title}: concluído.`);
      onDone?.(true);
      return true;
    } catch (err) {
      toast.push("error", err instanceof ApiError ? err.message : `${title}: falhou.`);
      return false;
    }
  }, [track, toast]);
  const value = useMemo(() => ({ track, submit }), [track, submit]);
  return (
    <Ctx.Provider value={value}>
      {children}
      {active && <OperationPanel key={active.jobId} jobId={active.jobId} title={active.title} onClose={(ok) => { active.onDone?.(ok); setActive(null); }} />}
    </Ctx.Provider>
  );
}

export function ApprovalsLink() {
  return <Link href="/tools" className="underline">Tools &amp; Aprovações</Link>;
}
