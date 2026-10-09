"use client";

import { Briefcase } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ListPage } from "@/components/ui/ListPage";
import { useAsync } from "@/lib/useAsync";
import { clientsService } from "@/services";
import type { Client } from "@/lib/domain";

export default function ClientsPage() {
  const { data, loading, error, reload } = useAsync(() => clientsService.list());
  const cols: Column<Client>[] = [
    { key: "n", header: "Cliente", primary: true, cell: (c) => <span className="font-semibold">{c.name}</span> },
    { key: "p", header: "Projetos", cell: (c) => <span className="tabular-nums">{c.projects}</span> },
  ];
  return (
    <ListPage title="Clientes" description="Clientes e respetivos projetos.">
      <DataTable caption="Clientes" columns={cols} rows={data} loading={loading} error={error} onRetry={reload} empty={{ icon: <Briefcase />, title: "Nenhum cliente." }} />
    </ListPage>
  );
}
