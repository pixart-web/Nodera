"use client";

import { useEffect, useRef, useState } from "react";
import { NoderaMark } from "@/components/brand/NoderaMark";
import { siteConfig, siteLinks } from "@/lib/site-config";
import { useInView, useReducedMotion } from "./hooks";
import { PrimaryCta, TechLink } from "./primitives";

// Hero topology, 1440×900 design space. Nodes sit mostly behind/right of the
// headline; `extra` nodes are depth only and are dropped on small screens.
const CX = 1140;
const CY = 440;
const NODES = [
  { x: 960, y: 210, label: "web-prod-01", kind: "server" },
  { x: 1150, y: 160, label: "web-prod-02", kind: "server" },
  { x: 1330, y: 236, label: "web-prod-03", kind: "server", left: true },
  { x: 1362, y: 392, label: "web-prod-04", kind: "server", warn: true, left: true },
  { x: 934, y: 352, label: "ctr · 86 running", kind: "container" },
  { x: 1322, y: 590, label: "postgres-prod", kind: "db", left: true },
  { x: 964, y: 604, label: "mariadb-shop", kind: "db" },
  { x: 1140, y: 728, label: "backup · offsite", kind: "backup" },
  { x: 920, y: 486, label: "uptime-checks", kind: "monitor" },
  { x: 1262, y: 808, label: "shop.example.com", kind: "site" },
  { x: 1420, y: 130, label: "", kind: "extra" },
  { x: 780, y: 120, label: "", kind: "extra" },
  { x: 720, y: 770, label: "", kind: "extra" },
  { x: 860, y: 872, label: "", kind: "extra" },
  { x: 1430, y: 690, label: "", kind: "extra" },
  { x: 600, y: 300, label: "", kind: "extra" },
] as const;

const PEERS: [number, number][] = [[0, 1], [1, 2], [2, 3], [5, 7], [6, 7], [6, 9], [11, 0], [12, 8], [13, 6], [14, 5], [10, 2], [15, 4]];

function route(x1: number, y1: number, x2: number, y2: number) {
  const mx = Math.round((x1 + x2) / 2);
  return `M${x1} ${y1}H${mx}V${y2}H${x2}`;
}

function useLiveTelemetry(active: boolean) {
  const reduced = useReducedMotion();
  const [t, setT] = useState({ health: 98.7, p95: 182, events: 1240 });
  useEffect(() => {
    if (!active || reduced) return;
    const id = window.setInterval(() => {
      setT((prev) => ({
        health: Math.round((98.62 + Math.random() * 0.16) * 100) / 100,
        p95: Math.round(176 + Math.random() * 14),
        events: prev.events + Math.round(4 + Math.random() * 9),
      }));
    }, 2400);
    return () => window.clearInterval(id);
  }, [active, reduced]);
  return t;
}

export function Hero() {
  const [ref, visible] = useInView<HTMLElement>({ once: false, rootMargin: "0px" });
  const stageRef = useRef<HTMLDivElement>(null);
  const reduced = useReducedMotion();
  const live = useLiveTelemetry(visible);

  // Cursor-reactive depth: a few pixels of parallax on fine pointers only.
  useEffect(() => {
    const host = ref.current;
    const stage = stageRef.current;
    if (!host || !stage || reduced || !window.matchMedia("(pointer: fine)").matches) return;
    let raf = 0;
    let mx = 0;
    let my = 0;
    const apply = () => { raf = 0; stage.style.setProperty("--mx", mx.toFixed(3)); stage.style.setProperty("--my", my.toFixed(3)); };
    const onMove = (e: PointerEvent) => {
      const r = host.getBoundingClientRect();
      mx = ((e.clientX - r.left) / r.width) * 2 - 1;
      my = ((e.clientY - r.top) / r.height) * 2 - 1;
      if (!raf) raf = requestAnimationFrame(apply);
    };
    host.addEventListener("pointermove", onMove);
    return () => { host.removeEventListener("pointermove", onMove); if (raf) cancelAnimationFrame(raf); };
  }, [ref, reduced]);

  return (
    <section ref={ref} id="top" className="hero" data-inview={visible} aria-labelledby="hero-title">
      {siteConfig.heroVideoUrl && !reduced ? (
        <video className="hero-video" src={siteConfig.heroVideoUrl} autoPlay muted loop playsInline preload="metadata" aria-hidden tabIndex={-1} />
      ) : null}
      <div className="hero-grid" aria-hidden />
      <div ref={stageRef} className="hero-stage" aria-hidden>
        <svg className="hero-topo" viewBox="0 0 1440 900" preserveAspectRatio="xMidYMid slice" focusable="false">
          <g className="topo-links">
            {NODES.map((n, i) => n.kind === "extra" ? null : (
              <g key={`l${i}`} style={{ ["--d" as string]: i }}>
                <path className="link" d={route(n.x, n.y, CX, CY)} pathLength={100} />
                <path className="pulse" d={route(n.x, n.y, CX, CY)} pathLength={100} />
              </g>
            ))}
            {PEERS.map(([a, b], i) => {
              const A = NODES[a]!;
              const B = NODES[b]!;
              return <path key={`p${i}`} className={`link link-peer ${A.kind === "extra" || B.kind === "extra" ? "is-extra" : ""}`} d={route(A.x, A.y, B.x, B.y)} pathLength={100} style={{ ["--d" as string]: i }} />;
            })}
          </g>
          <g className="topo-nodes">
            {NODES.map((n, i) => (
              <g key={`n${i}`} className={`hnode hnode-${n.kind} ${"warn" in n && n.warn ? "is-warn" : ""}`} transform={`translate(${n.x} ${n.y})`} style={{ ["--d" as string]: i }}>
                <rect className="hnode-ring" x="-9" y="-9" width="18" height="18" />
                <rect className="hnode-core" x="-3.5" y="-3.5" width="7" height="7" />
                {n.label ? <text className="hnode-label" x={"left" in n && n.left ? -16 : 16} y="4" textAnchor={"left" in n && n.left ? "end" : "start"}>{n.label}</text> : null}
              </g>
            ))}
          </g>
          <g className="control-layer" transform={`translate(${CX} ${CY})`}>
            <rect className="cl-halo" x="-150" y="-44" width="300" height="88" />
            <rect className="cl-body" x="-128" y="-28" width="256" height="56" />
            <g transform="translate(-112 -14) scale(.58)">
              <rect x="8" y="8" width="7" height="9" className="cl-live" />
              <rect x="8" y="19.5" width="7" height="9" /><rect x="8" y="31" width="7" height="9" />
              <path d="M17.5 8h6l7 32h-6z" /><rect x="33" y="8" width="7" height="32" />
            </g>
            <text className="cl-label" x="-76" y="-2">NODERA</text>
            <text className="cl-sub" x="-76" y="13">CONTROL LAYER · ONLINE</text>
          </g>
        </svg>
      </div>
      <div className="hero-vignette" aria-hidden />

      <div className="hero-content container">
        <div className="hero-mark"><NoderaMark size={44} animated title="NODERA" /></div>
        <p className="label hero-eyebrow">The control plane for modern infrastructure</p>
        <h1 id="hero-title" className="h1 hero-title">
          <span className="mask"><span>Infrastructure,</span></span>{" "}
          <span className="mask"><span>under control.</span></span>
        </h1>
        <p className="hero-sub">
          Nodera gives infrastructure teams one intelligent control layer for servers, applications, deployments, backups and operations.
        </p>
        <div className="hero-ctas">
          <PrimaryCta href={siteLinks.start}>Start with Nodera</PrimaryCta>
          <TechLink href="#platform">Explore the platform</TechLink>
        </div>
        <p className="hero-price mono">€99/month · Built for serious infrastructure</p>
      </div>

      <div className="hero-telemetry container" aria-label="Live telemetry, demonstration data">
        <span className="label tel-head"><i className="dot dot-ok live" aria-hidden /> System status · demo</span>
        <dl className="tel-list">
          <div><dt>Servers</dt><dd className="mono">24</dd></div>
          <div><dt>Health</dt><dd className="mono">{live.health.toFixed(2)}%</dd></div>
          <div><dt>Containers</dt><dd className="mono">86</dd></div>
          <div><dt>p95 latency</dt><dd className="mono">{live.p95} ms</dd></div>
          <div className="tel-hide-sm"><dt>Events</dt><dd className="mono">{live.events.toLocaleString("en-US")}</dd></div>
        </dl>
      </div>
    </section>
  );
}
