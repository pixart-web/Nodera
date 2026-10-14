"use client";

import { useRef } from "react";
import { NoderaMark } from "@/components/brand/NoderaMark";
import { useStickyProgress } from "./hooks";
import { LayerHeader } from "./primitives";

// Eight disconnected systems. (x0,y0) = scattered, each with its own drift;
// (x1,y1) = converged onto a ring around the Nodera layer. Units: % of stage.
const TOOLS = [
  { name: "Servers", meta: "ssh · 24 hosts", x0: 12, y0: 18, a: 0 },
  { name: "Docker", meta: "86 containers", x0: 78, y0: 12, a: 45 },
  { name: "WordPress", meta: "147 dashboards", x0: 88, y0: 52, a: 90 },
  { name: "Deployments", meta: "ci logs", x0: 70, y0: 86, a: 135 },
  { name: "Backups", meta: "cron + scripts", x0: 30, y0: 88, a: 180 },
  { name: "Monitoring", meta: "separate alerts", x0: 6, y0: 62, a: 225 },
  { name: "Domains", meta: "registrar panel", x0: 40, y0: 6, a: 270 },
  { name: "Alerts", meta: "email · chat · sms", x0: 54, y0: 40, a: 315 },
].map((t, i) => {
  const rad = ((t.a - 90) * Math.PI) / 180;
  return { ...t, i, x1: 50 + Math.cos(rad) * 36, y1: 50 + Math.sin(rad) * 36 };
});

export function Fragmentation() {
  const ref = useRef<HTMLElement>(null);
  useStickyProgress(ref);

  return (
    <section ref={ref} className="frag" aria-labelledby="frag-title" data-layer="01" data-layer-name="Fragmentation">
      <div className="frag-pin">
        <div className="container frag-grid">
          <div className="frag-copy">
            <LayerHeader
              n="01"
              name="THE PROBLEM"
              id="frag-title"
              title={<>Your infrastructure shouldn&apos;t live in twelve different tools.</>}
            />
            <div className="frag-captions">
              <p className="frag-cap frag-cap-a">Every system has its own console, its own alerts and its own idea of what is healthy. Nobody sees the whole picture until something breaks.</p>
              <p className="frag-cap frag-cap-b"><strong>Nodera brings them into one operational layer.</strong> One inventory, one timeline, one place to act.</p>
            </div>
          </div>

          <div className="frag-stage" role="img" aria-label="Eight disconnected infrastructure tools converge into one Nodera control layer.">
            <svg className="frag-lines" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden focusable="false">
              {TOOLS.map((t) => (
                <line key={t.name} x1="50" y1="50" x2={t.x1} y2={t.y1} vectorEffect="non-scaling-stroke" />
              ))}
            </svg>
            <div className="frag-core" aria-hidden>
              <NoderaMark size={40} />
              <span className="label">NODERA</span>
            </div>
            {TOOLS.map((t) => (
              <div
                key={t.name}
                className="frag-tool"
                aria-hidden
                style={{
                  ["--x0" as string]: t.x0, ["--y0" as string]: t.y0,
                  ["--x1" as string]: t.x1.toFixed(2), ["--y1" as string]: t.y1.toFixed(2),
                  ["--i" as string]: t.i,
                }}
              >
                <div className="frag-drift">
                  <span className="frag-led" />
                  <span className="frag-name">{t.name}</span>
                  <span className="frag-meta mono">{t.meta}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
