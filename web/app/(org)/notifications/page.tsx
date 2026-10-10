"use client";

import { Bell } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Badge } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ListPage } from "@/components/ui/ListPage";
import { ago } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { notificationsService } from "@/services";
import type { ApiNotification } from "@/lib/types";

export default function NotificationsPage() {
  const { data, loading, error, reload } = useAsync(() => notificationsService.list());
  const cols: Column<ApiNotification>[] = [
    { key: "t", header: "Notificação", primary: true, cell: (n) => <div><p className={n.read ? "text-nd-muted" : "font-semibold"}>{n.title}</p>{n.body && <p className="text-xs text-nd-muted">{n.body}</p>}</div> },
    { key: "k", header: "Tipo", hideOnMobile: true, cell: (n) => <Badge>{n.kind}</Badge> },
    { key: "w", header: "Quando", cell: (n) => <span className="text-nd-muted">{ago(n.created_at)}</span> },
    { key: "a", header: "", className: "text-right", hideOnMobile: true, cell: (n) => !n.read ? <Button size="sm" onClick={async () => { await notificationsService.markRead([n.id]); reload(); }}>Marcar lida</Button> : null },
  ];
  return (
    <ListPage title="Notificações" description="Resultados de operações, incidentes e avisos da plataforma." actions={<Button onClick={async () => { await notificationsService.markAll(); reload(); }}>Marcar tudo como lido</Button>}>
      <DataTable caption="Notificações" columns={cols} rows={data ?? []} loading={loading} error={error} onRetry={reload} empty={{ icon: <Bell />, title: "Sem notificações", description: "Aparecem aqui quando uma operação termina ou um alerta dispara." }} />
    </ListPage>
  );
}
