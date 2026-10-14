"use client";

import { useState } from "react";
import { incidentEvents, incidentSteps } from "./data";
import { useInView, useSequence } from "./hooks";
import { DemoTag, LayerHeader } from "./primitives";

export function Automation() {
  const [ref, inView] = useInView<HTMLElement>({ rootMargin: "0px 0px -25% 0px" });
  const [run, setRun] = useState(0);
  const shown = useSequence(incidentEvents.length, inView, 1100, run);
  const current = shown > 0 ? incidentEvents[shown - 1]!.step : -1;
  const done = shown >= incidentEvents.length;

  return (
    <section ref={ref} id="automation" className="layer auto" data-inview={inView} aria-labelledby="auto-title" data-layer="04" data-layer-name="Automation">
      <div className="container">
        <LayerHeader
          n="04"
          name="AUTOMATION"
          id="auto-title"
          title="From alert to action."
          lede="Know before your users do. Every incident moves through the same disciplined loop, and every action runs as an audited, rollback-capable operation."
        />

        <ol className="loop" aria-label="Operational loop">
          {incidentSteps.map((s, i) => (
            <li key={s} className={`loop-step ${i < current || (done && i <= current) ? "is-done" : ""} ${i === current && !done ? "is-active" : ""}`}>
              <span className="mono loop-n">0{i + 1}</span>
              <span className="loop-name">{s}</span>
            </li>
          ))}
        </ol>

        <div className="incident">
          <div className="incident-head">
            <p className="label">Simulated incident · web-prod-04</p>
            <span className={`incident-state mono ${done ? "t-ok" : "t-warn"}`}>{done ? "RESOLVED · 32s" : shown > 0 ? "IN PROGRESS" : "STANDBY"}</span>
            <DemoTag />
          </div>
          <ol className="events">
            {incidentEvents.map((e, i) => (
              <li key={e.t} className={`event ${i < shown ? "is-in" : ""} ${i === incidentEvents.length - 1 && i < shown ? "is-final" : ""}`} aria-hidden={i >= shown}>
                <span className="event-t mono">{e.t}</span>
                <span className="event-rail" aria-hidden />
                <span className="event-text">{e.text}</span>
                <span className="event-meta mono">{e.meta}</span>
              </li>
            ))}
          </ol>
          {done ? (
            <button type="button" className="tech-link incident-replay" onClick={() => setRun((r) => r + 1)}>
              <span>Replay incident</span>
            </button>
          ) : null}
        </div>
      </div>
    </section>
  );
}
