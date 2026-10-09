"use client";

import { LockKeyhole } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Badge, StatusText } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { Dropdown } from "@/components/ui/Dropdown";
import { ListPage } from "@/components/ui/ListPage";
import { useToast } from "@/components/ui/Toast";
import { useAsync } from "@/lib/useAsync";
import { certificatesService } from "@/services";
import type { Certificate } from "@/lib/domain";

export default function SslPage() {
  const toast = useToast();
  const { data, loading, error, reload } = useAsync(() => certificatesService.list());
  const soon = (w: string) => () => toast.push("info", `${w}: operação ainda não ligada ao backend.`);
  const cols: Column<Certificate>[] = [
    { key: "d", header: "Domain", primary: true, cell: (c) => <span className="font-semibold">{c.domain}</span> },
    { key: "i", header: "Issuer", cell: (c) => c.issuer },
    { key: "f", header: "Valid From", cell: (c) => <span className="text-nd-muted">{c.validFrom}</span> },
    { key: "e", header: "Expires", cell: (c) => <span className="text-nd-muted">{c.expires}</span> },
    { key: "r", header: "Days Remaining", cell: (c) => <span className={`tabular-nums ${c.daysRemaining < 45 ? "text-nd-warning" : ""}`}>{c.daysRemaining}</span> },
    { key: "s", header: "Status", cell: (c) => <StatusText status={c.status} /> },
    { key: "ar", header: "Auto Renewal", cell: (c) => <Badge tone={c.autoRenewal ? "green" : "orange"}>{c.autoRenewal ? "Ativo" : "Desligado"}</Badge> },
    { key: "a", header: "", hideOnMobile: true, className: "text-right", cell: (c) => (
      <Dropdown label={`Ações para ${c.domain}`} trigger={<span className="inline-flex h-8 items-center rounded-nd border border-nd-strong px-3 text-xs font-medium hover:bg-nd-hover">Ações</span>}
        items={["Inspect", "Renew", "Revoke"].map((l) => ({ label: l, danger: l === "Revoke", onSelect: soon(l) }))} />) },
  ];
  return (
    <ListPage title="SSL / Certificados" description="Validade, renovação e emissão de certificados."
      actions={<Button variant="primary" onClick={soon("Emitir SSL")}>Emitir certificado</Button>}>
      <DataTable caption="Certificados SSL" columns={cols} rows={data} loading={loading} error={error} onRetry={reload}
        empty={{ icon: <LockKeyhole />, title: "Nenhum certificado emitido.", action: { label: "Emitir SSL", onClick: soon("Emitir SSL") } }} />
    </ListPage>
  );
}
