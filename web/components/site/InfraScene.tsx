"use client";

import { useCallback, useRef, useState } from "react";
import { useStickyProgress } from "./hooks";
import { LayerHeader } from "./primitives";

// Bottom → top: the physical layer first, observability last. Copy is kept to
// what the platform actually does today (see README status table).
const LAYERS = [
  { name: "Dedicated servers", tag: "NODES", text: "Register nodes from any provider. A signed agent reports status and runs only allowlisted commands." },
  { name: "VPS", tag: "NODES", text: "The same inventory, controls and audit trail, whatever the provider or size." },
  { name: "Docker", tag: "CONTAINERS", text: "Containers run under the node agent. The control plane never holds the Docker socket." },
  { name: "Databases", tag: "DATA", text: "Provisioned per project and included in every backup and restore." },
  { name: "Applications", tag: "RUNTIME", text: "Registered, versioned and deployed through prechecks, health checks and automatic revert." },
  { name: "WordPress", tag: "SITES", text: "Install, clone, update with rollback and health reports, across every site you operate." },
  { name: "Domains & SSL", tag: "EDGE", text: "Domains, DNS and certificates tracked next to the sites that depend on them." },
  { name: "Backups", tag: "RESILIENCE", text: "Checksummed archives, off-site copies, verified restores." },
  { name: "Monitoring", tag: "SIGNALS", text: "Checks, deduplicated incidents and notifications from the same layer." },
];

export function InfraScene() {
  const ref = useRef<HTMLElement>(null);
  const [active, setActive] = useState(0);
  const onStep = useCallback((p: number) => {
    setActive(Math.min(LAYERS.length - 1, Math.floor(p * LAYERS.length * 0.999)));
  }, []);
  useStickyProgress(ref, onStep);
  const current = LAYERS[active]!;

  return (
    <section ref={ref} id="infrastructure" className="infra" aria-labelledby="infra-title" data-layer="05" data-layer-name="Infrastructure" style={{ ["--n" as string]: LAYERS.length }}>
      <div className="infra-pin">
        <div className="container infra-grid">
          <div className="infra-copy">
            <LayerHeader n="05" name="INFRASTRUCTURE" id="infra-title" title="Everything you run. One scene." />
            <ol className="infra-list">
              {LAYERS.map((l, i) => (
                <li key={l.name} className={i === active ? "is-active" : i < active ? "is-online" : ""} aria-current={i === active ? "step" : undefined}>
                  <span className="mono infra-i">{String(i + 1).padStart(2, "0")}</span>
                  <span className="infra-name">{l.name}</span>
                  <p className="infra-text">{l.text}</p>
                </li>
              ))}
            </ol>
          </div>

          <div className="iso-wrap" aria-hidden>
            <div className="iso">
              {LAYERS.map((l, i) => (
                <div key={l.name} className={`slab ${i === active ? "is-active" : i < active ? "is-online" : ""}`} style={{ ["--i" as string]: i }}>
                  <span className="slab-label mono">{l.tag}</span>
                  <span className="slab-grid" />
                </div>
              ))}
            </div>
            <p className="iso-caption">
              <span className="mono">LAYER {String(active + 1).padStart(2, "0")} · ONLINE</span>
              <strong>{current.name}</strong>
            </p>
          </div>
        </div>
      </div>
    </section>
  );
}
