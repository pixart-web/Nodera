"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Globe, Plus } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { StatusText, Badge } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ListPage } from "@/components/ui/ListPage";
import { Field, Input, Select } from "@/components/ui/Forms";
import { FormModal } from "@/components/ui/FormModal";
import { ConfirmDialog, Modal } from "@/components/ui/Overlay";
import { Alert, LoadingState } from "@/components/ui/Feedback";
import { usePlatform } from "@/components/providers/Platform";
import { useOperations } from "@/components/providers/Operations";
import { useToast } from "@/components/ui/Toast";
import { ApiError } from "@/lib/api";
import { toStatus } from "@/lib/status";
import { date } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { domainsService, projectsService } from "@/services";
import type { ApiDomain } from "@/lib/types";

function RecordsModal({ domain, onClose }: { domain: ApiDomain; onClose: () => void }) {
  const toast = useToast();
  const { can } = usePlatform();
  const recs = useAsync(() => domainsService.records(domain.id), [domain.id]);
  const [f, setF] = useState({ type: "A", name: "@", value: "", ttl: "300", priority: "10" });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [prop, setProp] = useState<Array<{ id: string; ok: boolean; text: string }> | null>(null);
  const manage = can("domains.manage");

  async function add() {
    setBusy(true); setErr(null);
    try {
      await domainsService.upsertRecord(domain.id, { type: f.type, name: f.name, value: f.value, ttl: Number(f.ttl) || 300, priority: f.type === "MX" ? Number(f.priority) : undefined });
      setF({ ...f, value: "" }); recs.reload();
    } catch (e) { setErr(e instanceof ApiError ? e.message : "Falhou"); } finally { setBusy(false); }
  }
  async function check() {
    try {
      const r = await domainsService.checkPropagation(domain.id);
      setProp(r.map((x) => ({ id: x.record.id, ok: x.propagated, text: `${x.record.type} ${x.record.name}${x.error ? " — " + x.error : ""}` })));
    } catch (e) { toast.push("error", e instanceof ApiError ? e.message : "Falhou"); }
  }
  return (
    <Modal open onClose={onClose} title={`Registos DNS — ${domain.name}`} size="lg" description={`Fornecedor DNS: ${domain.dns_provider}. Os registos guardados são a fonte de verdade; “Sincronizar” volta a enviá-los ao fornecedor.`}
      footer={<><Button onClick={check}>Verificar propagação</Button>{manage && <Button onClick={async () => { try { const r = await domainsService.sync(domain.id); toast.push("success", `${r.synced} registos sincronizados.`); } catch (e) { toast.push("error", e instanceof ApiError ? e.message : "Falhou"); } }}>Sincronizar</Button>}<Button variant="primary" onClick={onClose}>Fechar</Button></>}>
      <div className="space-y-4">
        {err && <Alert tone="error">{err}</Alert>}
        {prop && <ul className="space-y-1 text-sm">{prop.map((p) => <li key={p.id} className={p.ok ? "text-nd-success" : "text-nd-warning"}>{p.ok ? "✓ propagado" : "… a aguardar"} · {p.text}</li>)}</ul>}
        {recs.loading ? <LoadingState /> : (recs.data ?? []).length === 0 ? <p className="text-sm text-nd-muted">Sem registos. Adiciona o primeiro abaixo.</p> : (
          <table className="w-full text-sm"><thead><tr className="text-left text-xs text-nd-muted"><th className="pb-2">Tipo</th><th>Nome</th><th>Valor</th><th>TTL</th><th /></tr></thead>
            <tbody>{recs.data!.map((r) => (
              <tr key={r.id} className="border-t border-nd-border/50"><td className="py-2"><Badge>{r.type}</Badge></td><td className="font-mono text-xs">{r.name}</td><td className="max-w-[220px] truncate font-mono text-xs">{r.value}</td><td className="tabular-nums">{r.ttl}</td>
                <td className="text-right">{manage && <Button size="sm" variant="danger" onClick={async () => { try { await domainsService.deleteRecord(domain.id, r.id); recs.reload(); } catch (e) { toast.push("error", e instanceof ApiError ? e.message : "Falhou"); } }}>Remover</Button>}</td></tr>))}</tbody></table>
        )}
        {manage && (
          <form onSubmit={(e) => { e.preventDefault(); void add(); }} className="grid gap-3 border-t border-nd-border pt-4 sm:grid-cols-[90px_1fr_2fr_80px_auto]">
            <Field label="Tipo">{(id) => <Select id={id} value={f.type} onChange={(e) => setF({ ...f, type: e.target.value })}>{["A", "AAAA", "CNAME", "MX", "TXT", "CAA"].map((t) => <option key={t}>{t}</option>)}</Select>}</Field>
            <Field label="Nome">{(id) => <Input id={id} value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />}</Field>
            <Field label="Valor">{(id) => <Input id={id} value={f.value} onChange={(e) => setF({ ...f, value: e.target.value })} required />}</Field>
            <Field label="TTL">{(id) => <Input id={id} inputMode="numeric" value={f.ttl} onChange={(e) => setF({ ...f, ttl: e.target.value })} />}</Field>
            <div className="flex items-end"><Button type="submit" variant="primary" disabled={busy || !f.value}>Adicionar</Button></div>
          </form>
        )}
      </div>
    </Modal>
  );
}

function DomainsInner() {
  const router = useRouter();
  const params = useSearchParams();
  const { can } = usePlatform();
  const ops = useOperations();
  const { data, loading, error, reload } = useAsync(() => domainsService.list());
  const projects = useAsync(() => projectsService.list());
  const [adding, setAdding] = useState(false);
  const [f, setF] = useState({ name: "", project_id: "" });
  const [records, setRecords] = useState<ApiDomain | null>(null);
  const [del, setDel] = useState<ApiDomain | null>(null);
  const manage = can("domains.manage");
  useEffect(() => { if (params.get("new")) { setAdding(true); router.replace("/domains"); } }, [params, router]);

  const pname = (id: string | null) => projects.data?.find((p) => p.id === id)?.name ?? "—";
  const cols: Column<ApiDomain>[] = [
    { key: "d", header: "Domínio", primary: true, cell: (d) => <span className="font-semibold">{d.name}</span> },
    { key: "p", header: "Projeto", hideOnMobile: true, cell: (d) => pname(d.project_id) },
    { key: "dns", header: "DNS", cell: (d) => { const s = toStatus(d.dns_status === "ok" ? "ok" : d.dns_status); return <StatusText status={s.status} label={d.dns_status === "ok" ? "DNS OK" : d.dns_status === "pending" ? "A propagar" : "Mal configurado"} />; } },
    { key: "ssl", header: "SSL", cell: (d) => d.ssl_status === "none" ? <Badge>Sem SSL</Badge> : <StatusText {...toStatus(d.ssl_status)} /> },
    { key: "v", header: "Verificado", hideOnMobile: true, cell: (d) => <span className="text-nd-muted">{date(d.verified_at)}</span> },
    { key: "a", header: "", hideOnMobile: true, className: "text-right", cell: (d) => (
      <div className="flex justify-end gap-1.5"><Button size="sm" onClick={() => setRecords(d)}>DNS</Button>{manage && <Button size="sm" variant="danger" onClick={() => setDel(d)}>Remover</Button>}</div>) },
  ];
  return (
    <ListPage title="Domínios" description="DNS, SSL e associação a projetos."
      actions={manage ? <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setAdding(true)}>Adicionar domínio</Button> : undefined}>
      <DataTable caption="Domínios" columns={cols} rows={data ?? []} loading={loading} error={error} onRetry={reload}
        empty={{ icon: <Globe />, title: "Nenhum domínio configurado.", description: "Adiciona um domínio para gerir os seus registos DNS e certificado.", action: manage ? { label: "Adicionar domínio", onClick: () => setAdding(true) } : undefined }} />
      <FormModal open={adding} onClose={() => setAdding(false)} title="Adicionar domínio" description="Cria a zona DNS e regista o domínio. O SSL emite-se depois em SSL / Certificados." submitLabel="Adicionar" disabled={!f.name.trim()}
        onSubmit={async () => { await domainsService.add(f.name, f.project_id || undefined); setF({ name: "", project_id: "" }); reload(); }}>
        <Field label="Domínio" hint="Ex.: exemplo.pt (sem https://)">{(id) => <Input id={id} value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} required autoFocus />}</Field>
        <Field label="Projeto (opcional)">{(id) => <Select id={id} value={f.project_id} onChange={(e) => setF({ ...f, project_id: e.target.value })}><option value="">Nenhum</option>{(projects.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</Select>}</Field>
      </FormModal>
      {records && <RecordsModal domain={records} onClose={() => { setRecords(null); reload(); }} />}
      <ConfirmDialog open={!!del} onClose={() => setDel(null)} danger confirmLabel="Pedir remoção" title={`Remover “${del?.name}”?`}
        description="Remove o domínio, os registos DNS e revoga o certificado. Exige aprovação; nada acontece até ser aprovado."
        onConfirm={() => { if (del) void ops.submit(`Remover ${del.name}`, async () => (await domainsService.remove(del.id)) as never, reload); }} />
    </ListPage>
  );
}
export default function DomainsPage() { return <Suspense><DomainsInner /></Suspense>; }
