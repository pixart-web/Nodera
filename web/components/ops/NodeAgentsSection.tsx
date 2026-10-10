"use client";

import { useState } from "react";
import { Bot } from "lucide-react";
import { Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Badge, StatusText } from "@/components/ui/Status";
import { Alert, EmptyState, ErrorState, LoadingState } from "@/components/ui/Feedback";
import { Modal, ConfirmDialog } from "@/components/ui/Overlay";
import { Field, Select } from "@/components/ui/Forms";
import { FormModal } from "@/components/ui/FormModal";
import { usePlatform } from "@/components/providers/Platform";
import { useToast } from "@/components/ui/Toast";
import { ApiError } from "@/lib/api";
import { toStatus } from "@/lib/status";
import { ago } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { agentsAdminService } from "@/services";
import type { ApiNodeAgent } from "@/lib/types";

// Node Agent management: enrolment tokens are shown exactly once.
export function NodeAgentsSection({ nodes }: { nodes: Array<{ id: string; hostname: string }> }) {
  const { can } = usePlatform();
  const toast = useToast();
  const agents = useAsync(() => agentsAdminService.list());
  const [reg, setReg] = useState(false);
  const [nodeId, setNodeId] = useState("");
  const [token, setToken] = useState<{ token: string; expires_at: string } | null>(null);
  const [revoke, setRevoke] = useState<ApiNodeAgent | null>(null);
  const manage = can("infrastructure.manage");

  return (
    <Section title="Node Agents" description="Agentes por nó (pull, assinados, só comandos permitidos). Sem agente não há métricas reais do nó."
      actions={manage ? <Button size="sm" variant="primary" icon={<Bot className="h-4 w-4" />} onClick={() => setReg(true)}>Registar agente</Button> : undefined}>
      {agents.loading ? <LoadingState /> : agents.error ? <ErrorState message={agents.error} onRetry={agents.reload} /> : (agents.data ?? []).length === 0
        ? <EmptyState icon={<Bot />} title="Nenhum agente enrolado" description="Gera um token de enrolamento e corre `nodera-agent enroll` no nó." />
        : (
          <ul className="divide-y divide-nd-border/50">{agents.data!.map((a) => (
            <li key={a.id} className="flex flex-wrap items-center justify-between gap-3 py-3 text-sm">
              <span className="font-mono">{a.hostname || a.node_id.slice(0, 8)}</span>
              <StatusText {...toStatus(a.revoked_at ? "revoked" : a.status)} />
              <span className="text-xs text-nd-muted">v{a.version || "?"} · visto {ago(a.last_seen_at)}</span>
              <span className="flex gap-1">{a.capabilities.map((c) => <Badge key={c}>{c}</Badge>)}</span>
              {manage && !a.revoked_at && <Button size="sm" variant="danger" onClick={() => setRevoke(a)}>Revogar</Button>}
            </li>))}</ul>)}

      <FormModal open={reg} onClose={() => setReg(false)} title="Registar agente" description="Cria um token de uso único (30 min). Só é mostrado agora." submitLabel="Gerar token" disabled={!nodeId}
        onSubmit={async () => { const r = await agentsAdminService.register(nodeId); setToken(r); }}>
        <Field label="Nó">{(id) => <Select id={id} value={nodeId} onChange={(e) => setNodeId(e.target.value)} required><option value="">Escolhe…</option>{nodes.map((n) => <option key={n.id} value={n.id}>{n.hostname}</option>)}</Select>}</Field>
      </FormModal>
      {token && (
        <Modal open onClose={() => { setToken(null); agents.reload(); }} title="Token de enrolamento" size="lg" footer={<Button variant="primary" onClick={() => { setToken(null); agents.reload(); }}>Já copiei</Button>}>
          <Alert tone="warning" title="Copia agora — não volta a ser mostrado">Expira {ago(token.expires_at)} (30 min). Fica guardado apenas como hash.</Alert>
          <pre className="mt-3 overflow-x-auto rounded-nd bg-[#050C17] p-3 text-xs text-nd-text">{`nodera-agent enroll --api <URL da API> --token ${token.token}\nnodera-agent run`}</pre>
        </Modal>)}
      <ConfirmDialog open={!!revoke} onClose={() => setRevoke(null)} danger confirmLabel="Revogar" title="Revogar este agente?" description="O agente deixa de conseguir autenticar-se imediatamente; comandos pendentes não serão entregues."
        onConfirm={async () => { try { await agentsAdminService.revoke(revoke!.id); toast.push("success", "Agente revogado."); agents.reload(); } catch (e) { toast.push("error", e instanceof ApiError ? e.message : "Falhou"); } }} />
    </Section>
  );
}
