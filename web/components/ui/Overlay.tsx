"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { Button, IconButton } from "./Button";
import { cn } from "./cn";

const FOCUSABLE = 'a[href],button:not([disabled]),input,select,textarea,[tabindex]:not([tabindex="-1"])';

// Shared dialog behaviour: Esc to close, focus trap, focus restore, scroll lock.
function useDialog(open: boolean, onClose: () => void) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const previous = document.activeElement as HTMLElement | null;
    const node = ref.current;
    node?.querySelector<HTMLElement>(FOCUSABLE)?.focus();
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") { e.stopPropagation(); onClose(); }
      if (e.key !== "Tab" || !node) return;
      const items = Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE));
      if (items.length === 0) return;
      const first = items[0]!, last = items[items.length - 1]!;
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prevOverflow;
      previous?.focus?.();
    };
  }, [open, onClose]);
  return ref;
}

export function Modal({ open, onClose, title, description, children, footer, size = "md" }: {
  open: boolean; onClose: () => void; title: string; description?: string; children?: ReactNode; footer?: ReactNode; size?: "sm" | "md" | "lg";
}) {
  const ref = useDialog(open, onClose);
  if (!open || typeof document === "undefined") return null;
  return createPortal(
    <div className="fixed inset-0 z-[80] flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-[2px] nd-fade-in" onClick={onClose} aria-hidden />
      <div ref={ref} role="dialog" aria-modal="true" aria-label={title}
        className={cn("nd-pop-in relative flex max-h-[calc(100dvh-2rem)] w-full flex-col rounded-nd-lg border border-nd-strong bg-nd-surface shadow-pop", size === "sm" && "max-w-sm", size === "md" && "max-w-lg", size === "lg" && "max-w-2xl")}>
        <div className="flex shrink-0 items-start justify-between gap-4 border-b border-nd-border p-5">
          <div>
            <h2 className="text-base font-semibold text-nd-text">{title}</h2>
            {description && <p className="mt-1 text-sm text-nd-muted">{description}</p>}
          </div>
          <IconButton label="Fechar" onClick={onClose} className="-mr-2 -mt-1"><X className="h-4 w-4" /></IconButton>
        </div>
        {children && <div className="nd-scroll min-h-0 flex-1 overflow-y-auto p-5">{children}</div>}
        {footer && <div className="flex shrink-0 justify-end gap-2 border-t border-nd-border p-4">{footer}</div>}
      </div>
    </div>,
    document.body,
  );
}

export function Drawer({ open, onClose, title, children, side = "left", className }: {
  open: boolean; onClose: () => void; title: string; children: ReactNode; side?: "left" | "right"; className?: string;
}) {
  const ref = useDialog(open, onClose);
  if (!open || typeof document === "undefined") return null;
  return createPortal(
    <div className="fixed inset-0 z-[70]">
      <div className="absolute inset-0 bg-black/60 nd-fade-in" onClick={onClose} aria-hidden />
      <div ref={ref} role="dialog" aria-modal="true" aria-label={title}
        className={cn("absolute top-0 h-full w-[280px] max-w-[88vw] overflow-y-auto border-nd-border bg-nd-elevated shadow-pop",
          side === "left" ? "left-0 border-r animate-[nd-slide-in_var(--t-slow)_var(--ease)]" : "right-0 border-l animate-[nd-slide-in-right_var(--t-slow)_var(--ease)]", className)}>
        {children}
      </div>
    </div>,
    document.body,
  );
}

export function ConfirmDialog({ open, onClose, onConfirm, title, description, confirmLabel = "Confirmar", danger }: {
  open: boolean; onClose: () => void; onConfirm: () => void; title: string; description: string; confirmLabel?: string; danger?: boolean;
}) {
  return (
    <Modal open={open} onClose={onClose} title={title} description={description} size="sm"
      footer={<>
        <Button onClick={onClose}>Cancelar</Button>
        <Button variant={danger ? "danger" : "primary"} onClick={() => { onConfirm(); onClose(); }}>{confirmLabel}</Button>
      </>} />
  );
}
