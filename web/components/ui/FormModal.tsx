"use client";

import { useState, type FormEvent, type ReactNode } from "react";
import { Modal } from "./Overlay";
import { Button } from "./Button";
import { Alert } from "./Feedback";
import { ApiError } from "@/lib/api";

// A modal around a <form>: handles busy state, Enter-to-submit, and shows the
// API's own error message (never a guessed one) without closing on failure.
export function FormModal({ open, onClose, title, description, submitLabel = "Guardar", danger, onSubmit, children, size = "md", disabled }: {
  open: boolean; onClose: () => void; title: string; description?: string; submitLabel?: string; danger?: boolean;
  onSubmit: () => Promise<unknown>; children: ReactNode; size?: "sm" | "md" | "lg"; disabled?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true); setError(null);
    try { await onSubmit(); onClose(); }
    catch (err) { setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "A operação falhou"); }
    finally { setBusy(false); }
  }
  function close() { setError(null); onClose(); }

  return (
    <Modal open={open} onClose={close} title={title} description={description} size={size}
      footer={<>
        <Button onClick={close} disabled={busy}>Cancelar</Button>
        <Button variant={danger ? "danger" : "primary"} type="submit" form="nd-form-modal" disabled={busy || disabled}>{busy ? "A processar…" : submitLabel}</Button>
      </>}>
      <form id="nd-form-modal" onSubmit={submit} className="space-y-4">
        {error && <Alert tone="error">{error}</Alert>}
        {children}
      </form>
    </Modal>
  );
}
