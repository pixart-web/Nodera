"use client";

import { Users } from "lucide-react";
import { Avatar } from "@/components/ui/Avatar";
import { Badge, StatusText } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ListPage } from "@/components/ui/ListPage";
import { Button } from "@/components/ui/Button";
import { useToast } from "@/components/ui/Toast";
import { useAsync } from "@/lib/useAsync";
import { usersService } from "@/services";
import type { User } from "@/lib/domain";

export default function UsersPage() {
  const toast = useToast();
  const { data, loading, error, reload } = useAsync(() => usersService.list());
  const cols: Column<User>[] = [
    { key: "n", header: "Utilizador", primary: true, cell: (u) => <div className="flex items-center gap-3"><Avatar initial={u.name[0]!} color="#334155" round /><div><p className="font-semibold">{u.name}</p><p className="text-xs text-nd-muted">{u.email}</p></div></div> },
    { key: "r", header: "Role", cell: (u) => <Badge tone="blue">{u.role}</Badge> },
    { key: "l", header: "Última atividade", cell: (u) => <span className="text-nd-muted">{u.lastSeen}</span> },
    { key: "s", header: "Estado", cell: (u) => <StatusText status={u.status} /> },
  ];
  return (
    <ListPage title="Utilizadores" description="Gestão de acessos. A gestão real de membros e roles está em Definições." actions={<Button variant="primary" onClick={() => toast.push("info", "Convidar utilizador: operação ainda não ligada ao backend.")}>Convidar</Button>}>
      <DataTable caption="Utilizadores" columns={cols} rows={data} loading={loading} error={error} onRetry={reload} empty={{ icon: <Users />, title: "Nenhum utilizador." }} />
    </ListPage>
  );
}
