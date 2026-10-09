"use client";

import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from "lucide-react";
import { cn } from "./cn";

type Tone = "success" | "error" | "warning" | "info";
interface ToastItem { id: number; tone: Tone; message: string }
interface ToastApi { push: (tone: Tone, message: string) => void }

const Ctx = createContext<ToastApi>({ push: () => {} });
export const useToast = () => useContext(Ctx);

const ICON = { success: CheckCircle2, error: XCircle, warning: AlertTriangle, info: Info };
const COLOR = { success: "text-nd-success", error: "text-nd-danger", warning: "text-nd-warning", info: "text-nd-info" };

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const seq = useRef(0);
  const dismiss = useCallback((id: number) => setItems((l) => l.filter((t) => t.id !== id)), []);
  const push = useCallback((tone: Tone, message: string) => {
    const id = ++seq.current;
    setItems((l) => [...l.slice(-3), { id, tone, message }]);
    setTimeout(() => dismiss(id), 5000);
  }, [dismiss]);
  const api = useMemo(() => ({ push }), [push]);

  return (
    <Ctx.Provider value={api}>
      {children}
      <div aria-live="polite" className="pointer-events-none fixed bottom-4 right-4 z-[90] flex w-[min(92vw,360px)] flex-col gap-2">
        {items.map((t) => {
          const Icon = ICON[t.tone];
          return (
            <div key={t.id} role="status" className="nd-pop-in pointer-events-auto flex items-start gap-3 rounded-nd-lg border border-nd-strong bg-nd-raised p-3.5 shadow-pop">
              <Icon className={cn("mt-0.5 h-4 w-4 shrink-0", COLOR[t.tone])} aria-hidden />
              <p className="flex-1 text-sm text-nd-text">{t.message}</p>
              <button type="button" aria-label="Fechar notificação" onClick={() => dismiss(t.id)} className="text-nd-muted hover:text-nd-text"><X className="h-4 w-4" /></button>
            </div>
          );
        })}
      </div>
    </Ctx.Provider>
  );
}
