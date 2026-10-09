"use client";

import { Globe, Plus } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Badge, StatusText } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ListPage } from "@/components/ui/ListPage";
import { useToast } from "@/components/ui/Toast";
import { useAsync } from "@/lib/useAsync";
import { domainsService } from "@/services";
import type { Domain } from "@/lib/domain";

const DNS = { OK: ["DNS OK", "green"], PENDING: ["DNS Pending", "orange"], MISCONFIGURED: ["Misconfigured", "red"] } as const;
const SSL = { OK: ["SSL OK", "green"], PENDING: ["SSL Pending", "orange"], NONE: ["Sem SSL", "neutral"] } as const;

export default function DomainsPage() {
  const toast = useToast();
  const { data, loading, error, reload } = useAsync(() => domainsService.list());
  const soon = (w: string) => () => toast.push("info", `${w}: operação ainda não ligada ao backend.`);
  const cols: Column<Domain>[] = [
    { key: "d", header: "Domain", primary: true, cell: (d) => <span className="font-semibold">{d.name}</span> },
    { key: "p", header: "Project", cell: (d) => d.project },
    { key: "dns", header: "DNS", cell: (d) => <Badge tone={DNS[d.dns][1]}>{DNS[d.dns][0]}</Badge> },
    { key: "ssl", header: "SSL", cell: (d) => <Badge tone={SSL[d.ssl][1]}>{SSL[d.ssl][0]}</Badge> },
    { key: "st", header: "Status", cell: (d) => <StatusText status={d.status} /> },
    { key: "e", header: "Expires", cell: (d) => <span className="text-nd-muted">{d.expires}</span> },
    { key: "a", header: "", hideOnMobile: true, className: "text-right", cell: () => <Button size="sm" onClick={soon("Gerir domínio")}>Gerir</Button> },
  ];
  return (
    <ListPage title="Domínios" description="DNS, SSL e associação a projetos."
      actions={<Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={soon("Adicionar domínio")}>Adicionar domínio</Button>}>
      <DataTable caption="Domínios" columns={cols} rows={data} loading={loading} error={error} onRetry={reload}
        empty={{ icon: <Globe />, title: "Nenhum domínio configurado.", action: { label: "Adicionar domínio", onClick: soon("Adicionar domínio") } }} />
    </ListPage>
  );
}
