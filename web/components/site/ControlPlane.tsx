"use client";

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { topoEdges, topoNodes, topoTiers } from "./data";
import { useInView } from "./hooks";
import { DemoTag, LayerHeader } from "./primitives";

// Everything reachable upstream and downstream of `id`: what it depends on
// and what depends on it.
function dependencyChain(id: string): Set<string> {
  const out = new Set<string>([id]);
  const walk = (from: string, dir: 0 | 1) => {
    for (const e of topoEdges) {
      const [src, dst] = dir === 0 ? e : [e[1], e[0]];
      if (src === from && !out.has(dst)) { out.add(dst); walk(dst, dir); }
    }
  };
  walk(id, 0);
  walk(id, 1);
  return out;
}

interface Pt { x: number; y: number; w: number; h: number }

export function ControlPlane() {
  const [sectionRef, inView] = useInView<HTMLElement>();
  const boxRef = useRef<HTMLDivElement>(null);
  const nodeRefs = useRef(new Map<string, HTMLButtonElement>());
  const [pts, setPts] = useState<Record<string, Pt>>({});
  const [size, setSize] = useState({ w: 0, h: 0 });
  const [active, setActive] = useState<string | null>(null);

  const measure = useCallback(() => {
    const box = boxRef.current;
    if (!box) return;
    const b = box.getBoundingClientRect();
    const next: Record<string, Pt> = {};
    nodeRefs.current.forEach((el, id) => {
      const r = el.getBoundingClientRect();
      next[id] = { x: r.left - b.left, y: r.top - b.top, w: r.width, h: r.height };
    });
    setPts(next);
    setSize({ w: b.width, h: b.height });
  }, []);

  useLayoutEffect(() => { measure(); }, [measure]);
  useEffect(() => {
    const box = boxRef.current;
    if (!box || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => measure());
    ro.observe(box);
    return () => ro.disconnect();
  }, [measure]);

  const chain = useMemo(() => (active ? dependencyChain(active) : null), [active]);
  const activeLabel = active ? topoNodes.find((n) => n.id === active)?.label : null;

  const paths = topoEdges.map(([a, b]) => {
    const A = pts[a];
    const B = pts[b];
    if (!A || !B) return null;
    const vertical = B.y > A.y + A.h; // tiers stacked (small screens)
    let d: string;
    if (vertical) {
      const x1 = A.x + A.w / 2, y1 = A.y + A.h, x2 = B.x + B.w / 2, y2 = B.y;
      const my = (y1 + y2) / 2;
      d = `M${x1} ${y1}C${x1} ${my} ${x2} ${my} ${x2} ${y2}`;
    } else {
      const x1 = A.x + A.w, y1 = A.y + A.h / 2, x2 = B.x, y2 = B.y + B.h / 2;
      const mx = (x1 + x2) / 2;
      d = `M${x1} ${y1}C${mx} ${y1} ${mx} ${y2} ${x2} ${y2}`;
    }
    const on = chain ? chain.has(a) && chain.has(b) : false;
    return { key: `${a}-${b}`, d, on };
  });

  return (
    <section ref={sectionRef} id="platform" className="layer cp" data-inview={inView} aria-labelledby="cp-title" data-layer="02" data-layer-name="Control">
      <div className="container">
        <LayerHeader
          n="02"
          name="CONTROL PLANE"
          id="cp-title"
          title="One control plane. Every layer."
          lede="Servers, containers, applications, databases, websites, backups and monitoring live in one model, so a change anywhere shows its impact everywhere. Hover or focus any object to trace what it depends on and what depends on it."
        />
        <div className="cp-frame">
          <div className="cp-bar">
            <span className="label">Infrastructure topology</span>
            <span className="cp-readout mono" aria-live="polite">
              {activeLabel ? `${activeLabel} · ${(chain?.size ?? 1) - 1} dependencies` : "select an object"}
            </span>
            <DemoTag />
          </div>
          <div ref={boxRef} className={`cp-topo ${chain ? "has-focus" : ""}`} onMouseLeave={() => setActive(null)}>
            <svg className="cp-edges" width={size.w} height={size.h} aria-hidden focusable="false">
              {paths.map((p, i) => p ? (
                <path key={p.key} d={p.d} pathLength={1} className={p.on ? "on" : undefined} style={{ ["--i" as string]: i }} />
              ) : null)}
            </svg>
            {topoTiers.map((tier, t) => (
              <div key={tier} className="cp-tier" role="group" aria-label={tier} style={{ ["--t" as string]: t }}>
                <span className="cp-tier-label label">{String(t + 1).padStart(2, "0")} · {tier}</span>
                <div className="cp-tier-nodes">
                  {topoNodes.filter((n) => n.tier === t).map((n) => {
                    const state = chain ? (chain.has(n.id) ? (n.id === active ? "active" : "on") : "dim") : "idle";
                    return (
                      <button
                        key={n.id}
                        ref={(el) => { if (el) nodeRefs.current.set(n.id, el); else nodeRefs.current.delete(n.id); }}
                        type="button"
                        className={`cp-node mono is-${state} ${n.status === "degraded" ? "is-degraded" : ""}`}
                        aria-pressed={active === n.id}
                        aria-label={`${n.label}, ${tier}${n.status === "degraded" ? ", degraded" : ""}. Show dependencies.`}
                        onMouseEnter={() => setActive(n.id)}
                        onFocus={() => setActive(n.id)}
                        onBlur={() => setActive(null)}
                        onClick={() => setActive((cur) => (cur === n.id ? null : n.id))}
                      >
                        <i className={`dot ${n.status === "degraded" ? "dot-warn" : "dot-ok"}`} aria-hidden />
                        {n.label}
                      </button>
                    );
                  })}
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
