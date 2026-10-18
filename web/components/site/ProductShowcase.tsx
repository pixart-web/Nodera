"use client";

import { useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { demoBackups, demoDeploys, demoServers, demoSites, deployStages, incidentEvents } from "./data";
import { useInView } from "./hooks";
import { StatusDot, Window } from "./primitives";

const tone = (s: string) => (s === "HEALTHY" || s === "ACTIVE" || s === "COMPLETE" || s === "VERIFIED" ? "ok" : s === "RUNNING" ? "info" : "warn");

function Overview() {
  return (
    <div className="pv pv-overview">
      <div className="pv-stats">
        <div><p className="label">Infrastructure</p><p className="pv-big mono">24 <span>servers</span></p></div>
        <div><p className="label">Health</p><p className="pv-big mono t-ok">98.7%</p></div>
        <div><p className="label">Containers</p><p className="pv-big mono">86 <span>running</span></p></div>
        <div><p className="label">Sites</p><p className="pv-big mono">147</p></div>
      </div>
      <div className="pv-split">
        <table className="pv-table">
          <caption className="label">Production · Database</caption>
          <thead><tr><th scope="col">Server</th><th scope="col">Address</th><th scope="col">CPU</th><th scope="col">Status</th></tr></thead>
          <tbody>
            {demoServers.map((s) => (
              <tr key={s.name} className={s.status !== "HEALTHY" ? "is-warn" : undefined}>
                <td className="mono">{s.name}</td>
                <td className="mono dim">{s.ip}</td>
                <td><span className="meter" style={{ ["--v" as string]: `${s.cpu}%` }} aria-label={`${s.cpu}% CPU`} /></td>
                <td><span className={`pill pill-${tone(s.status)}`}>{s.status}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
        <svg className="pv-mini-topo" viewBox="0 0 220 180" aria-hidden focusable="false">
          {[[30, 30], [110, 20], [190, 34], [196, 110], [110, 160], [26, 120]].map(([x, y], i) => (
            <g key={i}>
              <path d={`M${x} ${y}L110 90`} />
              <rect x={x! - 5} y={y! - 5} width="10" height="10" className={i === 3 ? "w" : undefined} />
            </g>
          ))}
          <rect x="88" y="78" width="44" height="24" className="core" />
        </svg>
      </div>
    </div>
  );
}

function Operations() {
  return (
    <div className="pv">
      <p className="label">Operations · last 30 minutes</p>
      <ol className="pv-timeline">
        {incidentEvents.map((e) => (
          <li key={e.t}><span className="mono dim">{e.t}</span><StatusDot tone={e.step === 4 ? "ok" : e.step === 0 ? "warn" : "info"} /><span>{e.text}</span><span className="mono dim">{e.meta}</span></li>
        ))}
        <li><span className="mono dim">02:20:00</span><StatusDot tone="ok" /><span>backup.create completed</span><span className="mono dim">mariadb-shop · verified</span></li>
      </ol>
    </div>
  );
}

function IntelligenceView() {
  return (
    <div className="pv pv-ai">
      <div className="pv-ai-main">
        <p className="label">Incident summary · web-prod-04</p>
        <p className="pv-ai-text">CPU saturation started 13 minutes after <span className="mono">deployment #1842</span>. PHP workers on web-prod-04 are exhausted; Redis and the database are within normal range. Two public endpoints are affected.</p>
        <p className="label">Proposed plan · requires approval</p>
        <ol className="pv-plan">
          <li><StatusDot tone="ok" /> Roll back <span className="mono">#1842 → #1841</span></li>
          <li><StatusDot tone="idle" /> Run health check on <span className="mono">api.example.com</span></li>
          <li><StatusDot tone="idle" /> Open follow-up: worker limits for release 1842</li>
        </ol>
        <div className="chip-row"><span className="chip chip-accent">Approve plan</span><span className="chip">Explain this</span><span className="chip">Show impact</span></div>
      </div>
      <div className="pv-ai-side">
        <p className="label">Signals</p>
        <ul className="mono">
          <li><StatusDot tone="warn" /> cpu 94%</li>
          <li><StatusDot tone="warn" /> php-fpm 48/48</li>
          <li><StatusDot tone="ok" /> redis 1.2 ms</li>
          <li><StatusDot tone="ok" /> mariadb 0.8k qps</li>
        </ul>
      </div>
    </div>
  );
}

function Deployments() {
  return (
    <div className="pv">
      <p className="label">Deployment #1843 · client-portal · production</p>
      <ol className="pv-pipe">
        {deployStages.map((s, i) => (
          <li key={s} className={i < 4 ? "done" : i === 4 ? "gate" : ""}>
            <span className="mono">{s}</span>
            {i === 4 ? <span className="pv-gate">approval required</span> : null}
          </li>
        ))}
      </ol>
      <table className="pv-table">
        <thead><tr><th scope="col">Release</th><th scope="col">Target</th><th scope="col">Source</th><th scope="col">Status</th></tr></thead>
        <tbody>
          {demoDeploys.map((d) => (
            <tr key={d.id}><td className="mono">{d.id}</td><td className="mono">{d.target}</td><td className="mono dim">{d.source}</td><td><span className={`pill pill-${tone(d.status)}`}>{d.status}</span></td></tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Backups() {
  return (
    <div className="pv">
      <div className="pv-stats">
        <div><p className="label">Verified</p><p className="pv-big mono t-ok">99.98%</p></div>
        <div><p className="label">Off-site</p><p className="pv-big mono">3 / 4</p></div>
        <div><p className="label">Last restore</p><p className="pv-big mono">OK <span>· 6d ago</span></p></div>
      </div>
      <table className="pv-table">
        <thead><tr><th scope="col">Target</th><th scope="col">Kind</th><th scope="col">Size</th><th scope="col">Time</th><th scope="col">Verification</th></tr></thead>
        <tbody>
          {demoBackups.map((b) => (
            <tr key={b.target}><td className="mono">{b.target}</td><td className="dim">{b.kind}</td><td className="mono">{b.size}</td><td className="mono dim">{b.when}</td><td><span className={`pill pill-${tone(b.verify)}`}>{b.verify}{b.offsite ? " · OFF-SITE" : ""}</span></td></tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Sites() {
  return (
    <div className="pv">
      <p className="label">Managed websites · 147</p>
      <table className="pv-table">
        <thead><tr><th scope="col">Site</th><th scope="col">Stack</th><th scope="col">Health</th><th scope="col">TLS</th><th scope="col">Backup</th></tr></thead>
        <tbody>
          {demoSites.map((s) => (
            <tr key={s.domain}>
              <td className="mono">{s.domain}</td>
              <td className="dim">{s.stack}</td>
              <td><span className="meter meter-ok" style={{ ["--v" as string]: `${s.health}%` }} aria-label={`health ${s.health}`} /> <span className="mono">{s.health}</span></td>
              <td className={`mono ${parseInt(s.ssl, 10) < 20 ? "t-warn" : ""}`}>{s.ssl}</td>
              <td className="mono dim">{s.backup}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

const VIEWS: { id: string; label: string; path: string; node: ReactNode }[] = [
  { id: "overview", label: "Infrastructure", path: "nodera / infrastructure", node: <Overview /> },
  { id: "operations", label: "Operations", path: "nodera / operations", node: <Operations /> },
  { id: "intelligence", label: "Intelligence", path: "nodera / intelligence", node: <IntelligenceView /> },
  { id: "deployments", label: "Deployments", path: "nodera / deployments", node: <Deployments /> },
  { id: "backups", label: "Backups", path: "nodera / backups", node: <Backups /> },
  { id: "sites", label: "Sites", path: "nodera / sites", node: <Sites /> },
];

export function ProductShowcase() {
  const [ref, inView] = useInView<HTMLElement>();
  const [tab, setTab] = useState(0);
  const tabs = useRef<(HTMLButtonElement | null)[]>([]);

  const onKey = (e: KeyboardEvent) => {
    const dir = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    let next = tab;
    if (dir) next = (tab + dir + VIEWS.length) % VIEWS.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = VIEWS.length - 1;
    else return;
    e.preventDefault();
    setTab(next);
    tabs.current[next]?.focus();
  };
  const view = VIEWS[tab]!;

  return (
    <section ref={ref} id="product" className="layer product" data-inview={inView} aria-labelledby="product-title" data-layer="08" data-layer-name="Product">
      <div className="container">
        <header className="layer-head">
          <p className="label layer-tag"><span className="layer-n">THE PLATFORM</span><span className="layer-rule" aria-hidden />PRODUCT</p>
          <h2 id="product-title" className="h2">Dense where it matters. Calm everywhere else.</h2>
          <p className="lede">The Nodera interface is built like a control room: thin lines, live status, timelines and command panels instead of colourful tiles. The views below are demonstrations with illustrative data.</p>
        </header>

        <div className="tabs" role="tablist" aria-label="Product views" onKeyDown={onKey}>
          {VIEWS.map((v, i) => (
            <button
              key={v.id}
              ref={(el) => { tabs.current[i] = el; }}
              role="tab"
              type="button"
              id={`tab-${v.id}`}
              aria-selected={i === tab}
              aria-controls={`panel-${v.id}`}
              tabIndex={i === tab ? 0 : -1}
              className="tab"
              onClick={() => setTab(i)}
            >
              <span className="mono">0{i + 1}</span> {v.label}
            </button>
          ))}
        </div>
        <div role="tabpanel" id={`panel-${view.id}`} aria-labelledby={`tab-${view.id}`} className="product-panel" key={view.id}>
          <Window title={view.path} label={`${view.label} view (demonstration)`}>{view.node}</Window>
        </div>
      </div>
    </section>
  );
}
