"use client";

import { useState } from "react";
import Link from "next/link";
import { Briefcase, Plus } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ListPage } from "@/components/ui/ListPage";
import { Field, Input } from "@/components/ui/Forms";
import { FormModal } from "@/components/ui/FormModal";
import { ConfirmDialog } from "@/components/ui/Overlay";
import { usePlatform } from "@/components/providers/Platform";
import { useToast } from "@/components/ui/Toast";
import { ApiError } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { clientsService } from "@/services";
import type { ApiClient } from "@/lib/types";

export default function ClientsPage() {
  const { can } = usePlatform();
  const toast = useToast();
  const { data, loading, error, reload } = useAsync(() => clientsService.list());
  const [edit, setEdit] = useState<Partial<ApiClient> | null>(null);
  const [del, setDel] = useState<ApiClient | null>(null);
  const manage = can("clients.manage");

  const cols: Column<ApiClient>[] = [
    { key: "n", header: "Cliente", primary: true, cell: (c) => <span className="font-semibold">{c.name}</span> },
    { key: "e", header: "Contacto", hideOnMobile: true, cell: (c) => <span className="text-nd-muted">{c.contact_email || "—"}</span> },
    { key: "p", header: "Projetos", cell: (c) => <Link href={`/projects?client=${c.id}`} className="tabular-nums text-nd-primary-soft hover:underline">{c.project_count}</Link> },
    { key: "a", header: "", className: "text-right", hideOnMobile: true, cell: (c) => manage ? (
      <div className="flex justify-end gap-1.5"><Button size="sm" onClick={() => setEdit(c)}>Editar</Button><Button size="sm" variant="danger" onClick={() => setDel(c)}>Eliminar</Button></div>) : null },
  ];
  return (
    <ListPage title="Clientes" description="Quem é dono de que projetos."
      actions={manage ? <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setEdit({})}>Novo cliente</Button> : undefined}>
      <DataTable caption="Clientes" columns={cols} rows={data ?? []} loading={loading} error={error} onRetry={reload}
        empty={{ icon: <Briefcase />, title: "Ainda não há clientes", description: "Agrupa projetos por cliente para filtrar e faturar.", action: manage ? { label: "Novo cliente", onClick: () => setEdit({}) } : undefined }} />
      <FormModal open={!!edit} onClose={() => setEdit(null)} title={edit?.id ? "Editar cliente" : "Novo cliente"} submitLabel="Guardar" disabled={!edit?.name?.trim()}
        onSubmit={async () => { const b = { name: edit!.name!, contact_email: edit!.contact_email ?? "", notes: edit!.notes ?? "" }; if (edit!.id) await clientsService.update(edit!.id, b); else await clientsService.create(b); toast.push("success", "Cliente guardado."); reload(); }}>
        <Field label="Nome">{(id) => <Input id={id} value={edit?.name ?? ""} onChange={(e) => setEdit({ ...edit, name: e.target.value })} required autoFocus />}</Field>
        <Field label="Email de contacto">{(id) => <Input id={id} type="email" value={edit?.contact_email ?? ""} onChange={(e) => setEdit({ ...edit, contact_email: e.target.value })} />}</Field>
        <Field label="Notas">{(id) => <Input id={id} value={edit?.notes ?? ""} onChange={(e) => setEdit({ ...edit, notes: e.target.value })} />}</Field>
      </FormModal>
      <ConfirmDialog open={!!del} onClose={() => setDel(null)} danger confirmLabel="Eliminar" title={`Eliminar “${del?.name}”?`} description="Os projetos deste cliente não são eliminados; apenas deixam de estar associados a ele."
        onConfirm={async () => { try { await clientsService.remove(del!.id); toast.push("success", "Cliente eliminado."); reload(); } catch (e) { toast.push("error", e instanceof ApiError ? e.message : "Falhou"); } }} />
    </ListPage>
  );
}
