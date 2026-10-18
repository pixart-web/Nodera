"use client";

import { useState } from "react";
import { LockKeyhole } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Badge, StatusText } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { Dropdown } from "@/components/ui/Dropdown";
import { ListPage } from "@/components/ui/ListPage";
import { Field, Select } from "@/components/ui/Forms";
import { FormModal } from "@/components/ui/FormModal";
import { usePlatform } from "@/components/providers/Platform";
import { useOperations } from "@/components/providers/Operations";
import { toStatus } from "@/lib/status";
import { date } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { certificatesService, domainsService } from "@/services";
import type { ApiCertificate } from "@/lib/types";

export default function SslPage() {
  const { can } = usePlatform();
  const ops = useOperations();
  const { data, loading, error, reload } = useAsync(() => certificatesService.list());
  const domains = useAsync(() => domainsService.list());
  const [issuing, setIssuing] = useState(false);
  const [domainId, setDomainId] = useState("");
  const withCert = new Set((data ?? []).filter((c) => !["revoked", "error", "expired"].includes(c.status)).map((c) => c.domain_id));
  const free = (domains.data ?? []).filter((d) => !withCert.has(d.id));

  const cols: Column<ApiCertificate>[] = [
    { key: "d", header: "Domínio", primary: true, cell: (c) => <span className="font-semibold">{c.domain}</span> },
    { key: "i", header: "Emissor", hideOnMobile: true, cell: (c) => <span>{c.issuer || "—"}{c.provider === "mock" && <Badge tone="orange" className="ml-2">MOCK</Badge>}</span> },
    { key: "e", header: "Expira", cell: (c) => <span className="text-nd-muted">{date(c.not_after)}</span> },
    { key: "r", header: "Dias", cell: (c) => <span className={`tabular-nums ${(c.days_left ?? 999) < 30 ? "text-nd-warning" : ""}`}>{c.days_left ?? "—"}</span> },
    { key: "s", header: "Estado", cell: (c) => <StatusText {...toStatus(c.status)} /> },
    { key: "ar", header: "Renovação auto", hideOnMobile: true, cell: (c) => <Badge tone={c.auto_renew ? "green" : "orange"}>{c.auto_renew ? "Ativa" : "Desligada"}</Badge> },
    { key: "a", header: "", hideOnMobile: true, className: "text-right", cell: (c) => can("ssl.renew") ? (
      <Dropdown label={`Ações para ${c.domain}`} trigger={<span className="inline-flex h-8 items-center rounded-nd border border-nd-strong px-3 text-xs font-medium hover:bg-nd-hover">Ações</span>}
        items={[
          { label: "Renovar", disabled: ["revoked", "error", "expired"].includes(c.status), onSelect: () => ops.submit(`Renovar ${c.domain}`, () => certificatesService.renew(c.domain_id), reload) },
          { label: "Revogar", danger: true, disabled: !can("ssl.revoke") || ["revoked", "error", "expired"].includes(c.status), onSelect: () => ops.submit(`Revogar ${c.domain}`, () => certificatesService.revoke(c.domain_id), reload) },
        ]} />) : null },
  ];
  return (
    <ListPage title="SSL / Certificados" description="Validade, renovação e emissão de certificados. A chave privada nunca sai do cofre cifrado."
      actions={can("ssl.issue") ? <Button variant="primary" onClick={() => setIssuing(true)}>Emitir certificado</Button> : undefined}>
      <DataTable caption="Certificados SSL" columns={cols} rows={data ?? []} loading={loading} error={error} onRetry={reload}
        empty={{ icon: <LockKeyhole />, title: "Nenhum certificado emitido.", description: "Emite um certificado para um domínio já registado.", action: can("ssl.issue") ? { label: "Emitir certificado", onClick: () => setIssuing(true) } : undefined }} />
      <FormModal open={issuing} onClose={() => setIssuing(false)} title="Emitir certificado" description="Corre como operação com progresso em direto." submitLabel="Emitir" disabled={!domainId}
        onSubmit={async () => { await ops.submit("Emitir certificado", () => certificatesService.issue(domainId), reload); setDomainId(""); }}>
        {free.length === 0 ? <p className="text-sm text-nd-muted">Todos os domínios já têm certificado, ou ainda não registaste nenhum domínio.</p> :
          <Field label="Domínio">{(id) => <Select id={id} value={domainId} onChange={(e) => setDomainId(e.target.value)} required><option value="">Escolhe…</option>{free.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}</Select>}</Field>}
      </FormModal>
    </ListPage>
  );
}
