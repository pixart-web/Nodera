"use client";

import { Database } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Badge, StatusText } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ListPage } from "@/components/ui/ListPage";
import { ConfirmDialog } from "@/components/ui/Overlay";
import { useToast } from "@/components/ui/Toast";
import { useState } from "react";
import { useAsync } from "@/lib/useAsync";
import { backupsService } from "@/services";
import type { Backup } from "@/lib/domain";

export default function BackupsPage() {
  const toast = useToast();
  const { data, loading, error, reload } = useAsync(() => backupsService.list());
  const [del, setDel] = useState<Backup | null>(null);
  const soon = (w: string) => () => toast.push("info", `${w}: operação ainda não ligada ao backend.`);
  const cols: Column<Backup>[] = [
    { key: "p", header: "Project", primary: true, cell: (b) => <span className="font-semibold">{b.project}</span> },
    { key: "t", header: "Type", cell: (b) => <Badge>{b.type}</Badge> },
    { key: "s", header: "Size", cell: (b) => b.size },
    { key: "c", header: "Created", cell: (b) => <span className="text-nd-muted">{b.created}</span> },
    { key: "st", header: "Status", cell: (b) => <StatusText status={b.status} label="Completo" /> },
    { key: "r", header: "Retention", cell: (b) => b.retention },
    { key: "a", header: "", hideOnMobile: true, className: "text-right", cell: (b) => (
      <div className="flex justify-end gap-1.5"><Button size="sm" onClick={soon("Restore")}>Restore</Button><Button size="sm" onClick={soon("Download")}>Download</Button><Button size="sm" variant="danger" onClick={() => setDel(b)}>Delete</Button></div>) },
  ];
  return (
    <ListPage title="Backups" description="Cópias de segurança de sites e bases de dados." actions={<Button variant="primary" onClick={soon("Criar backup")}>Criar backup</Button>}>
      <DataTable caption="Backups" columns={cols} rows={data} loading={loading} error={error} onRetry={reload}
        empty={{ icon: <Database />, title: "Nenhum backup encontrado.", action: { label: "Criar backup", onClick: soon("Criar backup") } }} />
      <ConfirmDialog open={!!del} onClose={() => setDel(null)} danger confirmLabel="Eliminar" title="Eliminar backup?" description={`Vais eliminar o backup de ${del?.project}. (Demonstração — nada será eliminado.)`} onConfirm={soon("Eliminar backup")} />
    </ListPage>
  );
}
