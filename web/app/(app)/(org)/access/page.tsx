"use client";

import { useState } from "react";
import { Copy, KeyRound, Pencil, Plus, Power, Trash2, UserCog } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { useAction } from "@/lib/useAction";
import { PageHeader } from "@/components/ui/PageHeader";
import { Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Alert } from "@/components/ui/Feedback";
import { Badge } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ConfirmDialog, Modal } from "@/components/ui/Overlay";
import { Field, Input } from "@/components/ui/Forms";
import { RowMenu } from "@/components/ui/RowMenu";
import { useToast } from "@/components/ui/Toast";
import { StatusBadge } from "@/components/StatusBadge";
import type { AdminAPIToken, APIToken, CreatedAPIToken, ServiceAccount } from "@/lib/types";

const parseScopes = (raw: string): string[] => raw.split(",").map((s) => s.trim()).filter(Boolean);

function Scopes({ items }: { items: string[] }) {
  return <div className="flex max-w-sm flex-wrap gap-1">{items.map((s) => <Badge key={s}>{s}</Badge>)}</div>;
}

// Shown once, right after a token is minted — the API never returns the raw
// value again (only its hash is stored).
function NewTokenModal({ token, onClose }: { token: string | null; onClose: () => void }) {
  const toast = useToast();
  return (
    <Modal open={!!token} onClose={onClose} title="Token criado" description="Copia-o agora — não voltará a ser mostrado."
      footer={<><Button icon={<Copy className="h-4 w-4" />} onClick={() => { navigator.clipboard?.writeText(token ?? "").then(() => toast.push("success", "Token copiado."), () => toast.push("error", "Não foi possível copiar.")); }}>Copiar</Button><Button variant="primary" onClick={onClose}>Já guardei</Button></>}>
      <code className="block break-all rounded-nd border border-nd-border bg-[#050C17] p-3 font-mono text-xs text-nd-success">{token}</code>
    </Modal>
  );
}

export default function AccessPage() {
  const myTokens = useApi(() => api.get<APIToken[]>("/api/v1/api-tokens"), []);
  const serviceAccounts = useApi(() => api.get<ServiceAccount[]>("/api/v1/service-accounts"), []);
  // Org-wide token visibility needs organization.manage — FORBIDDEN means
  // "section not available", not an error to show.
  const orgTokens = useApi(() => api.get<AdminAPIToken[]>("/api/v1/organization/api-tokens").catch((e) => {
    if (e instanceof ApiError && e.code === "FORBIDDEN") return null;
    throw e;
  }), []);

  const row = useAction();
  const [newToken, setNewToken] = useState<string | null>(null);

  // --- my tokens ---
  const tokenAction = useAction();
  const [tokenForm, setTokenForm] = useState(false);
  const [tName, setTName] = useState(""); const [tScopes, setTScopes] = useState("");
  const [renaming, setRenaming] = useState<APIToken | null>(null); const [renameValue, setRenameValue] = useState("");
  const renameAction = useAction();
  const [revoking, setRevoking] = useState<{ id: string; name: string; org: boolean } | null>(null);

  async function createToken(e: React.FormEvent) {
    e.preventDefault();
    let created: CreatedAPIToken | null = null;
    if (await tokenAction.run(async () => { created = await api.post<CreatedAPIToken>("/api/v1/api-tokens", { name: tName, scopes: parseScopes(tScopes) }); }, undefined, "Não foi possível criar o token") && created) {
      setNewToken((created as CreatedAPIToken).token); setTName(""); setTScopes(""); setTokenForm(false); myTokens.reload();
    }
  }
  async function rename(e: React.FormEvent) {
    e.preventDefault();
    if (!renaming) return;
    if (await renameAction.run(() => api.put(`/api/v1/api-tokens/${renaming.id}`, { name: renameValue }), "Token renomeado.")) { setRenaming(null); myTokens.reload(); orgTokens.reload(); }
  }
  async function revoke() {
    if (!revoking) return;
    // A failed revoke just means the reload shows the true state.
    await row.run(() => api.del(revoking.org ? `/api/v1/organization/api-tokens/${revoking.id}` : `/api/v1/api-tokens/${revoking.id}`), "Token revogado.");
    myTokens.reload(); orgTokens.reload(); setRevoking(null);
  }

  // --- service accounts ---
  const saAction = useAction();
  const [saForm, setSaForm] = useState<{ sa: ServiceAccount | null } | null>(null);
  const [saName, setSaName] = useState(""); const [saDesc, setSaDesc] = useState("");
  const [issuing, setIssuing] = useState<ServiceAccount | null>(null);
  const issueAction = useAction();
  const [iName, setIName] = useState(""); const [iScopes, setIScopes] = useState("");
  const [deletingSA, setDeletingSA] = useState<ServiceAccount | null>(null);

  function openSAForm(sa: ServiceAccount | null) { setSaForm({ sa }); setSaName(sa?.name ?? ""); setSaDesc(sa?.description ?? ""); saAction.setError(null); }
  async function saveSA(e: React.FormEvent) {
    e.preventDefault();
    if (!saForm) return;
    const sa = saForm.sa;
    if (await saAction.run(() => (sa ? api.put(`/api/v1/service-accounts/${sa.id}`, { name: saName, description: saDesc }) : api.post("/api/v1/service-accounts", { name: saName, description: saDesc })), sa ? "Conta de serviço atualizada." : "Conta de serviço criada.")) { setSaForm(null); serviceAccounts.reload(); }
  }
  async function toggleSA(sa: ServiceAccount) {
    const enable = sa.status !== "active";
    await row.run(() => (enable ? api.post(`/api/v1/service-accounts/${sa.id}/enable`) : api.del(`/api/v1/service-accounts/${sa.id}`)), enable ? "Conta ativada." : "Conta desativada — os tokens foram revogados.");
    serviceAccounts.reload(); orgTokens.reload();
  }
  async function deleteSA() {
    if (!deletingSA) return;
    if (await row.run(() => api.del(`/api/v1/service-accounts/${deletingSA.id}/permanent`), "Conta de serviço eliminada.")) serviceAccounts.reload();
    setDeletingSA(null);
  }
  async function issue(e: React.FormEvent) {
    e.preventDefault();
    if (!issuing) return;
    let created: CreatedAPIToken | null = null;
    if (await issueAction.run(async () => { created = await api.post<CreatedAPIToken>(`/api/v1/service-accounts/${issuing.id}/api-tokens`, { name: iName, scopes: parseScopes(iScopes) }); }, undefined, "Não foi possível emitir o token") && created) {
      setNewToken((created as CreatedAPIToken).token); setIName(""); setIScopes(""); setIssuing(null); orgTokens.reload();
    }
  }

  const myCols: Column<APIToken>[] = [
    { key: "name", header: "Nome", primary: true, cell: (t) => <span className="font-mono text-xs font-medium">{t.name}</span> },
    { key: "prefix", header: "Prefixo", cell: (t) => <span className="font-mono text-xs text-nd-muted">{t.token_prefix}…</span> },
    { key: "scopes", header: "Scopes", cell: (t) => <Scopes items={t.scopes} /> },
    { key: "used", header: "Último uso", cell: (t) => <span className="text-xs text-nd-faint">{t.last_used_at ? new Date(t.last_used_at).toLocaleString("pt-PT") : "nunca"}</span> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (t) => (
      <RowMenu label={`Ações para ${t.name}`} items={[
        { label: "Renomear", icon: <Pencil />, onSelect: () => { setRenaming(t); setRenameValue(t.name); renameAction.setError(null); } },
        { label: "Revogar", icon: <Trash2 />, danger: true, separatorBefore: true, onSelect: () => setRevoking({ id: t.id, name: t.name, org: false }) },
      ]} />) },
  ];
  const saCols: Column<ServiceAccount>[] = [
    { key: "name", header: "Nome", primary: true, cell: (sa) => <span className="font-mono text-sm font-medium">{sa.name}</span> },
    { key: "desc", header: "Descrição", cell: (sa) => <span className="text-nd-muted">{sa.description || "—"}</span> },
    { key: "st", header: "Estado", cell: (sa) => <StatusBadge status={sa.status} /> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (sa) => (
      <RowMenu label={`Ações para ${sa.name}`} items={[
        { label: "Emitir token", icon: <KeyRound />, disabled: sa.status !== "active", onSelect: () => { setIssuing(sa); issueAction.setError(null); } },
        { label: "Editar", icon: <Pencil />, onSelect: () => openSAForm(sa) },
        { label: sa.status === "active" ? "Desativar" : "Ativar", icon: <Power />, onSelect: () => toggleSA(sa) },
        { label: "Eliminar (desativa primeiro)", icon: <Trash2 />, danger: true, separatorBefore: true, disabled: sa.status === "active", onSelect: () => setDeletingSA(sa) },
      ]} />) },
  ];
  const orgCols: Column<AdminAPIToken>[] = [
    { key: "name", header: "Nome", primary: true, cell: (t) => <span className="font-mono text-xs font-medium">{t.name}</span> },
    { key: "owner", header: "Dono", cell: (t) => <span className="text-xs text-nd-muted">{t.owner_label} <span className="text-nd-faint">({t.owner_type})</span></span> },
    { key: "scopes", header: "Scopes", cell: (t) => <Scopes items={t.scopes} /> },
    { key: "act", header: "", className: "text-right", hideOnMobile: true, cell: (t) => <Button size="sm" variant="danger" onClick={() => setRevoking({ id: t.id, name: t.name, org: true })}>Revogar</Button> },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="Acessos" description="Tokens de API e contas de serviço. Os scopes nunca podem exceder as permissões de quem os concede." />
      {row.error && <Alert tone="error">{row.error}</Alert>}

      <Section title="Os meus tokens de API" actions={<Button variant="primary" size="sm" icon={<Plus className="h-4 w-4" />} onClick={() => { setTokenForm(true); tokenAction.setError(null); }}>Criar token</Button>}>
        <DataTable caption="Tokens de API" columns={myCols} rows={myTokens.data} loading={myTokens.loading} error={myTokens.error} onRetry={myTokens.reload}
          empty={{ icon: <KeyRound />, title: "Ainda não tens tokens de API", action: { label: "Criar token", onClick: () => setTokenForm(true) } }} />
      </Section>

      <Section title="Contas de serviço" description="Identidades não humanas com os seus próprios tokens." actions={<Button variant="primary" size="sm" icon={<Plus className="h-4 w-4" />} onClick={() => openSAForm(null)}>Criar conta</Button>}>
        <DataTable caption="Contas de serviço" columns={saCols} rows={serviceAccounts.data} loading={serviceAccounts.loading} error={serviceAccounts.error} onRetry={serviceAccounts.reload}
          empty={{ icon: <UserCog />, title: "Nenhuma conta de serviço", action: { label: "Criar conta", onClick: () => openSAForm(null) } }} />
      </Section>

      {orgTokens.data && (
        <Section title="Todos os tokens da organização" description="Visível apenas com organization.manage.">
          <DataTable caption="Tokens da organização" columns={orgCols} rows={orgTokens.data} loading={false} error={null} empty={{ icon: <KeyRound />, title: "Sem tokens nesta organização" }} />
        </Section>
      )}

      <NewTokenModal token={newToken} onClose={() => setNewToken(null)} />

      <Modal open={tokenForm} onClose={() => setTokenForm(false)} title="Criar token de API" description="Os scopes têm de ser um subconjunto das tuas permissões."
        footer={<><Button onClick={() => setTokenForm(false)}>Cancelar</Button><Button variant="primary" type="submit" form="token-form" disabled={tokenAction.busy}>{tokenAction.busy ? "A criar…" : "Criar"}</Button></>}>
        <form id="token-form" onSubmit={createToken} className="space-y-4">
          {tokenAction.error && <Alert tone="error">{tokenAction.error}</Alert>}
          <Field label="Nome">{(id) => <Input id={id} value={tName} onChange={(e) => setTName(e.target.value)} required />}</Field>
          <Field label="Scopes (vírgulas)">{(id) => <Input id={id} className="font-mono" value={tScopes} onChange={(e) => setTScopes(e.target.value)} placeholder="infrastructure.read, audit.read" required />}</Field>
        </form>
      </Modal>

      <Modal open={!!renaming} onClose={() => setRenaming(null)} size="sm" title="Renomear token" description="Só o nome muda — os scopes ficam fixos desde a criação."
        footer={<><Button onClick={() => setRenaming(null)}>Cancelar</Button><Button variant="primary" type="submit" form="rename-form" disabled={renameAction.busy}>Guardar</Button></>}>
        <form id="rename-form" onSubmit={rename} className="space-y-4">
          {renameAction.error && <Alert tone="error">{renameAction.error}</Alert>}
          <Field label="Nome">{(id) => <Input id={id} value={renameValue} onChange={(e) => setRenameValue(e.target.value)} required />}</Field>
        </form>
      </Modal>

      <Modal open={!!saForm} onClose={() => setSaForm(null)} title={saForm?.sa ? "Editar conta de serviço" : "Criar conta de serviço"}
        footer={<><Button onClick={() => setSaForm(null)}>Cancelar</Button><Button variant="primary" type="submit" form="sa-form" disabled={saAction.busy}>{saAction.busy ? "A guardar…" : "Guardar"}</Button></>}>
        <form id="sa-form" onSubmit={saveSA} className="space-y-4">
          {saAction.error && <Alert tone="error">{saAction.error}</Alert>}
          <Field label="Nome">{(id) => <Input id={id} value={saName} onChange={(e) => setSaName(e.target.value)} required />}</Field>
          <Field label="Descrição">{(id) => <Input id={id} value={saDesc} onChange={(e) => setSaDesc(e.target.value)} />}</Field>
        </form>
      </Modal>

      <Modal open={!!issuing} onClose={() => setIssuing(null)} title={`Emitir token para ${issuing?.name ?? ""}`}
        footer={<><Button onClick={() => setIssuing(null)}>Cancelar</Button><Button variant="primary" type="submit" form="issue-form" disabled={issueAction.busy}>{issueAction.busy ? "A emitir…" : "Emitir"}</Button></>}>
        <form id="issue-form" onSubmit={issue} className="space-y-4">
          {issueAction.error && <Alert tone="error">{issueAction.error}</Alert>}
          <Field label="Nome do token">{(id) => <Input id={id} value={iName} onChange={(e) => setIName(e.target.value)} required />}</Field>
          <Field label="Scopes (vírgulas)">{(id) => <Input id={id} className="font-mono" value={iScopes} onChange={(e) => setIScopes(e.target.value)} required />}</Field>
        </form>
      </Modal>

      <ConfirmDialog open={!!revoking} onClose={() => setRevoking(null)} onConfirm={revoke} danger confirmLabel="Revogar" title="Revogar token?" description={`“${revoking?.name}” deixa de funcionar de imediato.`} />
      <ConfirmDialog open={!!deletingSA} onClose={() => setDeletingSA(null)} onConfirm={deleteSA} danger confirmLabel="Eliminar" title="Eliminar conta de serviço?" description={`“${deletingSA?.name}” e todos os tokens que alguma vez emitiu serão eliminados definitivamente.`} />
    </div>
  );
}
