"use client";

import { demoMetrics as m } from "./data";
import { useCountUp, useInView } from "./hooks";
import { LayerHeader, StatusDot, Window } from "./primitives";

function Metric({ label, value, decimals = 0, suffix = "", active, tone, sub }: { label: string; value: number; decimals?: number; suffix?: string; active: boolean; tone?: "ok" | "warn" | "bad"; sub?: { label: string; value: number; tone: "ok" | "warn" | "bad" }[] }) {
  const v = useCountUp(value, active, decimals);
  return (
    <div className="metric">
      <dt className="label">{label}</dt>
      <dd>
        <span className={`metric-v mono ${tone ? `t-${tone}` : ""}`}>{v}{suffix}</span>
        {sub ? (
          <span className="metric-sub mono">
            {sub.map((s) => (
              <span key={s.label}><StatusDot tone={s.tone} /> {s.value} {s.label}</span>
            ))}
          </span>
        ) : null}
      </dd>
    </div>
  );
}

export function Intelligence() {
  const [ref, inView] = useInView<HTMLElement>();
  const health = useCountUp(m.health, inView, 1, 1800);

  return (
    <section ref={ref} id="intelligence" className="layer intel" data-inview={inView} aria-labelledby="intel-title" data-layer="03" data-layer-name="Visibility">
      <div className="container">
        <LayerHeader
          n="03"
          name="VISIBILITY · INTELLIGENCE"
          id="intel-title"
          title="Infrastructure that explains itself."
          lede="See every layer of your infrastructure. Understand what changed. Nodera correlates telemetry, deployments and events, then tells you what matters, in plain language, next to the object it concerns."
        />

        <div className="intel-grid">
          <Window title="nodera / overview" className="intel-panel">
            <div className="intel-health">
              <div>
                <p className="label">Infrastructure health</p>
                <p className="intel-health-v mono">{health}<span>%</span></p>
              </div>
              <div className="intel-bar" aria-hidden>
                <span style={{ ["--w" as string]: `${m.health}%` }} />
              </div>
            </div>
            <dl className="metrics">
              <Metric label="Servers" value={m.servers} active={inView} sub={[{ label: "healthy", value: m.healthy, tone: "ok" }, { label: "degraded", value: m.degraded, tone: "warn" }]} />
              <Metric label="Deployments" value={m.deployments} active={inView} sub={[{ label: "successful", value: m.successful, tone: "ok" }, { label: "failed", value: m.failed, tone: "bad" }]} />
              <Metric label="Managed websites" value={m.sites} active={inView} />
              <Metric label="Containers" value={m.containers} active={inView} />
              <Metric label="Backup success" value={m.backupSuccess} decimals={2} suffix="%" active={inView} tone="ok" />
              <Metric label="Incidents" value={m.incidents} active={inView} sub={[{ label: "resolved", value: m.resolved, tone: "ok" }, { label: "open", value: m.incidents - m.resolved, tone: "warn" }]} />
            </dl>
          </Window>

          <aside className="intel-ai" aria-label="Nodera Intelligence anomaly summary (demonstration)">
            <p className="label intel-ai-head"><span className="spark" aria-hidden /> Nodera intelligence</p>
            <p className="intel-ai-count"><span className="mono">3</span> anomalies detected</p>
            <div className="anomaly">
              <p className="mono anomaly-host"><StatusDot tone="warn" /> web-prod-04</p>
              <p>CPU saturation increased <strong>41%</strong> over 17 minutes.</p>
              <p className="label">Likely cause</p>
              <p>PHP worker exhaustion after <span className="mono">deployment #1842</span>.</p>
              <div className="chip-row">
                <a href="#ai-demo" className="chip">Investigate</a>
                <a href="#platform" className="chip">Show affected sites</a>
                <a href="#automation" className="chip">Suggest remediation</a>
              </div>
            </div>
            <ul className="anomaly-mini mono">
              <li><StatusDot tone="warn" /> api.example.com · p95 2.4s</li>
              <li><StatusDot tone="info" /> studio.example.org · TLS expires in 12 days</li>
            </ul>
          </aside>
        </div>
      </div>
    </section>
  );
}
