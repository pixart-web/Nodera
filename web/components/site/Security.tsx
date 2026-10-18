"use client";

import { auditEvents } from "./data";
import { useInView, useSequence } from "./hooks";
import { DemoTag, LayerHeader } from "./primitives";

const FEATURES = [
  ["Role-based access", "Owner, admin, member and custom roles. Permissions can never exceed the person granting them."],
  ["Audit trails", "Append-only, tenant-scoped, filterable. Every important action leaves a record."],
  ["Approval workflows", "Production changes and dangerous operations wait for a human decision, once."],
  ["Secrets management", "Encrypted at rest, never returned over the API, never written to logs."],
  ["Isolation", "Every organisation is its own tenant. Agents only ever run allowlisted commands."],
  ["Secure agents", "Signed in both directions, with replay protection. Unknown keys are dropped."],
  ["Encrypted backups", "Off-site copies with a pinned host key; Nodera's own backups are encrypted and read-back verified."],
  ["Operational visibility", "Who did what, to which object, under which approval, at what time."],
] as const;

// Agents around the control plane in a 600×440 space. The last one is untrusted.
const AGENTS = [
  { x: 110, y: 90, label: "web-prod-01" },
  { x: 300, y: 50, label: "web-prod-02" },
  { x: 490, y: 90, label: "db-prod-01" },
  { x: 520, y: 290, label: "web-prod-04" },
  { x: 300, y: 390, label: "backup-01" },
  { x: 80, y: 300, label: "203.0.113.47", untrusted: true },
];

export function Security() {
  const [ref, inView] = useInView<HTMLElement>({ rootMargin: "0px 0px -20% 0px" });
  const step = useSequence(AGENTS.length, inView, 520);

  return (
    <section ref={ref} id="security" className="layer sec" data-inview={inView} aria-labelledby="sec-title" data-layer="06" data-layer-name="Security">
      <div className="container">
        <LayerHeader
          n="06"
          name="SECURITY"
          id="sec-title"
          title="Control without compromise."
          lede="Operational speed is worthless if you can’t prove who changed what. Nodera is built so that the secure path is the only path."
        />
        <div className="sec-grid">
          <dl className="sec-features">
            {FEATURES.map(([k, v]) => (
              <div key={k} className="sec-f">
                <dt>{k}</dt>
                <dd>{v}</dd>
              </div>
            ))}
          </dl>

          <div className="sec-visual">
            <svg className="sec-topo" viewBox="0 0 600 440" role="img" aria-label="Agents connect to the control plane and are verified; an unknown host is rejected." focusable="false">
              {AGENTS.map((a, i) => {
                const state = i >= step ? "pending" : a.untrusted ? "rejected" : "verified";
                return (
                  <g key={a.label} className={`sa is-${state}`}>
                    <path className="sa-link" d={`M${a.x} ${a.y}L300 220`} pathLength={1} />
                    <g transform={`translate(${a.x} ${a.y})`}>
                      <rect className="sa-node" x="-7" y="-7" width="14" height="14" />
                      <text className="sa-label" x={a.x > 300 ? -14 : 14} y={a.y < 220 ? -14 : 26} textAnchor={a.x > 300 ? "end" : "start"}>{a.label}</text>
                      <text className="sa-state" x={a.x > 300 ? -14 : 14} y={a.y < 220 ? -1 : 39} textAnchor={a.x > 300 ? "end" : "start"}>
                        {state === "verified" ? "SIGNATURE VERIFIED" : state === "rejected" ? "REJECTED · UNKNOWN KEY" : "HANDSHAKE…"}
                      </text>
                    </g>
                  </g>
                );
              })}
              <g transform="translate(300 220)" className="sa-core">
                <rect x="-62" y="-24" width="124" height="48" />
                <text x="0" y="-2" textAnchor="middle" className="sa-core-t">CONTROL PLANE</text>
                <text x="0" y="13" textAnchor="middle" className="sa-core-s">ed25519 · audited</text>
              </g>
            </svg>

            <div className="audit" aria-label="Audit event stream (demonstration)">
              <div className="audit-head"><span className="label">Audit stream</span><DemoTag /></div>
              <div className="audit-viewport">
                <ul className="audit-track mono">
                  {[...auditEvents, ...auditEvents].map((e, i) => (
                    <li key={i} className="audit-ev" aria-hidden={i >= auditEvents.length}>
                      <p className="audit-a">AUDIT EVENT · <span className="t-accent">{e.action}</span></p>
                      <p>user: {e.user}</p>
                      <p>target: {e.target}</p>
                      <p>change: {e.detail}</p>
                      <p>status: <span className={e.status === "REJECTED" ? "t-bad" : e.status === "PENDING" ? "t-warn" : "t-ok"}>{e.status}</span></p>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
