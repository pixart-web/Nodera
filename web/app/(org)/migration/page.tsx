"use client";

import { useState } from "react";
import { Card, Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { PageHeader } from "@/components/ui/PageHeader";
import { Alert, NotConnected } from "@/components/ui/Feedback";
import { Checkbox, Field, Input, Select } from "@/components/ui/Forms";
import { Stepper } from "@/components/ui/Stepper";
import { useToast } from "@/components/ui/Toast";

const STEPS = ["Source", "Destination", "Configuration", "Migration", "Validation", "Final"];
const SOURCES = ["cPanel", "FTP", "SFTP", "Backup", "UpdraftPlus", "URL", "Manual upload"];
const MIGRATE = ["Files", "Database", "Media", "Plugins", "Themes", "Users", "URLs"];
const VALIDATE = ["HTTP", "SSL", "Database", "WordPress", "Images", "Links"];

export default function MigrationPage() {
  const toast = useToast();
  const [step, setStep] = useState(0);
  const [source, setSource] = useState("cPanel");

  return (
    <div className="space-y-6">
      <PageHeader title="Migrar WordPress" description="Assistente de migração de sites WordPress para a Nodera." />
      <NotConnected what="Interface de pré-visualização. O motor de migração não está implementado — nenhuma operação é executada." />
      <Card className="p-5"><Stepper steps={STEPS} current={step} /></Card>
      <Section title={STEPS[step]!}>
        {step === 0 && <fieldset><legend className="sr-only">Origem</legend><div className="grid grid-cols-2 gap-3 sm:grid-cols-4">{SOURCES.map((s) => (
          <label key={s} className={`cursor-pointer rounded-nd border p-3 text-center text-sm transition-colors ${source === s ? "border-nd-primary bg-nd-primary/10 text-nd-text" : "border-nd-border text-nd-muted hover:border-nd-strong"}`}>
            <input type="radio" name="source" className="sr-only" checked={source === s} onChange={() => setSource(s)} />{s}</label>))}</div></fieldset>}
        {step === 1 && <div className="grid gap-4 sm:grid-cols-2"><Field label="Destino">{(id) => <Select id={id}><option>Novo projeto</option><option>Projeto existente</option></Select>}</Field><Field label="Nome do projeto">{(id) => <Input id={id} placeholder="meu-site" />}</Field></div>}
        {step === 2 && <div className="grid gap-4 sm:grid-cols-2"><Field label="Domínio">{(id) => <Input id={id} placeholder="site.pt" />}</Field><Field label="PHP">{(id) => <Select id={id}><option>8.3</option><option>8.2</option></Select>}</Field><Field label="Base de dados">{(id) => <Select id={id}><option>MariaDB (nova)</option></Select>}</Field><Field label="SSL">{(id) => <Select id={id}><option>Let's Encrypt</option></Select>}</Field></div>}
        {step === 3 && <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">{MIGRATE.map((m) => <Checkbox key={m} label={m} defaultChecked />)}</div>}
        {step === 4 && <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">{VALIDATE.map((v) => <div key={v} className="rounded-nd border border-nd-border bg-nd-elevated p-3 text-sm"><p>{v}</p><p className="text-xs text-nd-faint">Por executar</p></div>)}</div>}
        {step === 5 && <Alert tone="info" title="Fim da pré-visualização">Nenhuma migração foi executada. Quando o motor existir, esta etapa mostrará o resultado real.</Alert>}
        <div className="mt-6 flex justify-between">
          <Button disabled={step === 0} onClick={() => setStep((s) => s - 1)}>Anterior</Button>
          {step < STEPS.length - 1
            ? <Button variant="primary" onClick={() => setStep((s) => s + 1)}>Seguinte</Button>
            : <Button variant="primary" onClick={() => toast.push("info", "Migração: operação ainda não ligada ao backend.")}>Iniciar migração</Button>}
        </div>
      </Section>
    </div>
  );
}
