"use client";

import { Boxes, ExternalLink, Plus } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Badge, StatusText } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { Dropdown } from "@/components/ui/Dropdown";
import { ListPage } from "@/components/ui/ListPage";
import { useToast } from "@/components/ui/Toast";
import { useAsync } from "@/lib/useAsync";
import { wordpressService } from "@/services";
import type { WordPressSite } from "@/lib/domain";

export default function WordPressPage() {
  const toast = useToast();
  const { data, loading, error, reload } = useAsync(() => wordpressService.list());
  const soon = (w: string) => () => toast.push("info", `${w}: operação ainda não ligada ao backend.`);
  const cols: Column<WordPressSite>[] = [
    { key: "name", header: "Site", primary: true, cell: (s) => <div><p className="font-semibold">{s.name}</p><p className="text-xs text-nd-muted">{s.domain}</p></div> },
    { key: "wp", header: "WordPress", cell: (s) => s.wpVersion },
    { key: "php", header: "PHP", cell: (s) => s.php },
    { key: "woo", header: "WooCommerce", cell: (s) => (s.woocommerce ? <Badge tone="purple">Ativo</Badge> : <span className="text-nd-faint">—</span>) },
    { key: "ssl", header: "SSL", cell: (s) => <Badge tone={s.ssl === "OK" ? "green" : "orange"}>{s.ssl === "OK" ? "Válido" : "Pendente"}</Badge> },
    { key: "backup", header: "Backup", cell: (s) => <span className="text-nd-muted">{s.lastBackup ?? "—"}</span> },
    { key: "status", header: "Estado", cell: (s) => <StatusText status={s.status} /> },
    { key: "act", header: "", hideOnMobile: true, className: "text-right", cell: (s) => (
      <div className="flex items-center justify-end gap-1.5">
        <a href={`https://${s.domain}`} target="_blank" rel="noreferrer"><Button size="sm" icon={<ExternalLink className="h-3.5 w-3.5" />}>Abrir</Button></a>
        <Button size="sm" onClick={soon("WP Admin")}>WP Admin</Button>
        <Dropdown label={`Operações em ${s.name}`} trigger={<span className="inline-flex h-8 items-center rounded-nd border border-nd-strong px-3 text-xs font-medium text-nd-text hover:bg-nd-hover">Gerir</span>}
          items={["Backup", "Update", "Clone", "Restore"].map((l) => ({ label: l, onSelect: soon(l) })).concat([{ label: "Delete", onSelect: soon("Delete") }])} />
      </div>) },
  ];
  return (
    <ListPage title="Sites WordPress" description="Instalações WordPress geridas. Install, Migrate, Clone, Backup, Restore, Update e Delete serão ligados ao backend."
      actions={<Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={soon("Novo WordPress")}>Novo WordPress</Button>}>
      <DataTable caption="Sites WordPress" columns={cols} rows={data} loading={loading} error={error} onRetry={reload}
        empty={{ icon: <Boxes />, title: "Nenhum site WordPress", description: "Instala ou migra o primeiro site.", action: { label: "Novo WordPress", onClick: soon("Novo WordPress") } }} />
    </ListPage>
  );
}
