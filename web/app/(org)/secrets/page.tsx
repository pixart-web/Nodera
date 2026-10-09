"use client";

import { useState } from "react";
import { KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { useAction } from "@/lib/useAction";
import { PageHeader } from "@/components/ui/PageHeader";
import { Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Alert } from "@/components/ui/Feedback";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ConfirmDialog, Modal } from "@/components/ui/Overlay";
import { Field, Input } from "@/components/ui/Forms";
import { RowMenu } from "@/components/ui/RowMenu";
import type { SecretMeta } from "@/lib/types";

export default function SecretsPage() {
  const secrets = useApi(() => api.get<SecretMeta[]>("/api/v1/secrets"), []);
  const create = useAction();
  const edit = useAction();
  const del = useAction();

  const [showForm, setShowForm] = useState(false);
  const [key, setKey] = useState(""); const [value, setValue] = useState(""); const [description, setDescription] = useState("");
  const [editing, setEditing] = useState<SecretMeta | null>(null);
  const [editDescription, setEditDescription] = useState("");
  const [deleting, setDeleting] = useState<SecretMeta | null>(null);

  const unavailable = secrets.error?.includes("not configured");

  async function save(e: React.FormEvent) {
    e.preventDefault();
    const ok = await create.run(() => api.put(`/api/v1/secrets/${encodeURIComponent(key)}`, { value, description }), "Secret guardado.", "Não foi possível guardar o secret");
    if (ok) { setKey(""); setValue(""); setDescription(""); setShowForm(false); secrets.reload(); }
  }
  async function saveDescription(e: React.FormEvent) {
    e.preventDefault();
    if (!editing) return;
    const ok = await edit.run(() => api.patch(`/api/v1/secrets/${encodeURIComponent(editing.key)}/description`, { description: editDescription }), "Descrição atualizada.");
    if (ok) { setEditing(null); secrets.reload(); }
  }
  async function remove() {
    if (!deleting) return;
    const ok = await del.run(() => api.del(`/api/v1/secrets/${encodeURIComponent(deleting.key)}`), "Secret eliminado.");
    if (ok) secrets.reload();
    setDeleting(null);
  }

  const columns: Column<SecretMeta>[] = [
    { key: "key", header: "Chave", primary: true, cell: (s) => <span className="font-mono text-xs text-nd-text">{s.key}</span> },
    { key: "desc", header: "Descrição", cell: (s) => <span className="text-nd-muted">{s.description || "—"}</span> },
    { key: "upd", header: "Atualizado", cell: (s) => <span className="text-xs text-nd-faint">{new Date(s.updated_at).toLocaleString("pt-PT")}</span> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (s) => (
      <RowMenu label={`Ações para ${s.key}`} items={[
        { label: "Editar descrição", icon: <Pencil />, onSelect: () => { setEditing(s); setEditDescription(s.description); edit.setError(null); } },
        { label: "Eliminar", icon: <Trash2 />, danger: true, separatorBefore: true, onSelect: () => setDeleting(s) },
      ]} />) },
  ];

  return (
    <div>
      <PageHeader title="Secrets" description="Cifrados em repouso (AES-256-GCM). Os valores nunca são mostrados depois de guardados."
        actions={!unavailable && <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => { setShowForm(true); create.setError(null); }}>Novo secret</Button>} />

      {unavailable && <Alert tone="warning" title="Módulo de secrets não configurado">Define NODERA_SECRETS_ENCRYPTION_KEY no servidor para o ativar.</Alert>}
      {!unavailable && del.error && <Alert tone="error" className="mb-4">{del.error}</Alert>}

      {!unavailable && (
        <Section title="Secrets da organização" description="Referências cifradas usadas por providers, tools e integrações.">
          <DataTable caption="Secrets" columns={columns} rows={secrets.data} loading={secrets.loading} error={secrets.error} onRetry={secrets.reload}
            empty={{ icon: <KeyRound />, title: "Nenhum secret guardado", description: "Guarda credenciais de forma cifrada para as usares por referência.", action: { label: "Novo secret", onClick: () => setShowForm(true) } }} />
        </Section>
      )}

      <Modal open={showForm} onClose={() => setShowForm(false)} title="Novo secret" description="O valor é cifrado e nunca mais é mostrado."
        footer={<><Button onClick={() => setShowForm(false)}>Cancelar</Button><Button variant="primary" type="submit" form="secret-form" disabled={create.busy}>{create.busy ? "A guardar…" : "Guardar"}</Button></>}>
        <form id="secret-form" onSubmit={save} className="space-y-4">
          {create.error && <Alert tone="error">{create.error}</Alert>}
          <Field label="Chave">{(id) => <Input id={id} className="font-mono" value={key} onChange={(e) => setKey(e.target.value)} placeholder="ai_provider.openai.api_key" required />}</Field>
          <Field label="Valor">{(id) => <Input id={id} type="password" value={value} onChange={(e) => setValue(e.target.value)} required autoComplete="off" />}</Field>
          <Field label="Descrição">{(id) => <Input id={id} value={description} onChange={(e) => setDescription(e.target.value)} />}</Field>
        </form>
      </Modal>

      <Modal open={!!editing} onClose={() => setEditing(null)} title="Editar descrição" description="Só a descrição muda — o valor do secret nunca é reenviado por este formulário."
        footer={<><Button onClick={() => setEditing(null)}>Cancelar</Button><Button variant="primary" type="submit" form="secret-edit" disabled={edit.busy}>{edit.busy ? "A guardar…" : "Guardar"}</Button></>}>
        <form id="secret-edit" onSubmit={saveDescription} className="space-y-4">
          {edit.error && <Alert tone="error">{edit.error}</Alert>}
          <Field label={editing?.key ?? "Descrição"}>{(id) => <Input id={id} value={editDescription} onChange={(e) => setEditDescription(e.target.value)} />}</Field>
        </form>
      </Modal>

      <ConfirmDialog open={!!deleting} onClose={() => setDeleting(null)} onConfirm={remove} danger confirmLabel="Eliminar"
        title="Eliminar secret?" description={`“${deleting?.key}” será eliminado definitivamente. Quem o usar por referência deixará de funcionar.`} />
    </div>
  );
}
