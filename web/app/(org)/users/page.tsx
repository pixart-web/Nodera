"use client";

import Link from "next/link";
import { Users } from "lucide-react";
import { Avatar } from "@/components/ui/Avatar";
import { Badge } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ListPage } from "@/components/ui/ListPage";
import { Button } from "@/components/ui/Button";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import type { Member } from "@/lib/types";

export default function UsersPage() {
  const { data, loading, error, reload } = useAsync(() => api.get<Member[]>("/api/v1/organization/members"));
  const cols: Column<Member & { id: string }>[] = [
    { key: "n", header: "Utilizador", primary: true, cell: (u) => <div className="flex items-center gap-3"><Avatar initial={(u.display_name || u.email)[0]!.toUpperCase()} color="#334155" round /><div><p className="font-semibold">{u.display_name}</p><p className="text-xs text-nd-muted">{u.email}</p></div></div> },
    { key: "r", header: "Roles", cell: (u) => <div className="flex flex-wrap gap-1">{u.roles.length ? u.roles.map((r) => <Badge key={r.role_id} tone="blue">{r.name}</Badge>) : <span className="text-nd-faint">—</span>}</div> },
  ];
  return (
    <ListPage title="Utilizadores" description="Membros desta organização e os seus roles. Convites e roles geridos em Acessos." actions={<Link href="/access"><Button variant="primary">Gerir acessos</Button></Link>}>
      <DataTable caption="Utilizadores" columns={cols} rows={(data ?? []).map((m) => ({ ...m, id: m.user_id }))} loading={loading} error={error} onRetry={reload} empty={{ icon: <Users />, title: "Nenhum utilizador." }} />
    </ListPage>
  );
}
