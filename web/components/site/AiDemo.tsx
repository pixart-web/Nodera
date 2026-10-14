"use client";

import { useState } from "react";
import { aiAffected, aiQuestion, aiSignals } from "./data";
import { useInView, useSequence } from "./hooks";
import { DemoTag, StatusDot } from "./primitives";

const STEPS = 9;

export function AiDemo() {
  const [ref, inView] = useInView<HTMLElement>({ rootMargin: "0px 0px -30% 0px" });
  const [run, setRun] = useState(0);
  const [review, setReview] = useState(false);
  const step = useSequence(STEPS, inView, 720, run);
  const show = (n: number) => step >= n;
  const done = step >= STEPS;

  return (
    <section ref={ref} id="ai-demo" className="layer ai" data-inview={inView} aria-labelledby="ai-title" data-layer="03" data-layer-name="Intelligence">
      <div className="container ai-grid">
        <div className="ai-copy">
          <p className="label">Root-cause analysis</p>
          <h2 id="ai-title" className="h2">Ask your infrastructure. Get reasoning, not chat.</h2>
          <p className="lede">
            Nodera&apos;s intelligence layer reads the same inventory, telemetry, deployments and audit trail your team does. It explains what it found, shows the impact, and proposes a plan.
          </p>
          <ul className="ai-points">
            <li><span className="mono">01</span> Natural-language infrastructure queries</li>
            <li><span className="mono">02</span> Anomaly explanations and incident summaries</li>
            <li><span className="mono">03</span> Proposed actions that a human approves, step by step</li>
          </ul>
        </div>

        <div className="terminal" role="group" aria-label="Demonstration of a Nodera root-cause analysis">
          <div className="window-bar">
            <span className="window-dots" aria-hidden><i /><i /><i /></span>
            <span className="window-title mono">nodera intelligence</span>
            <DemoTag />
          </div>
          <div className="term-body mono">
            <p className={`term-prompt ${show(1) ? "is-in" : ""}`}>
              <span className="term-caret" aria-hidden>&gt;</span>{" "}
              <span className="typed" style={{ ["--n" as string]: aiQuestion.length }}>{aiQuestion}</span>
            </p>

            {show(2) ? (
              <p className="term-status">
                {step < 7 ? <><span className="spinner" aria-hidden /> ANALYSING INFRASTRUCTURE…</> : <><StatusDot tone="ok" /> ANALYSIS COMPLETE · 4 signals correlated</>}
              </p>
            ) : null}

            <ul className="term-signals">
              {aiSignals.map((s, i) => show(3 + i) ? (
                <li key={s.label} className="term-line">
                  <StatusDot tone={s.tone === "warn" ? "warn" : s.tone === "ok" ? "ok" : "info"} />
                  <span className="sig-label">{s.label}</span>
                  <span className="sig-value">{s.value}</span>
                </li>
              ) : null)}
            </ul>

            {show(7) ? (
              <div className="term-block term-line">
                <p className="term-h">LIKELY ROOT CAUSE</p>
                <p className="term-text">Deployment #1842 increased PHP worker utilisation by 38%.</p>
              </div>
            ) : null}

            {show(8) ? (
              <div className="term-block term-line">
                <p className="term-h">AFFECTED SERVICES</p>
                <p className="term-text">{aiAffected.join("   ")}</p>
              </div>
            ) : null}

            {show(9) ? (
              <div className="term-block term-line">
                <p className="term-h">RECOMMENDED ACTION</p>
                <p className="term-text">Rollback deployment #1842 → release #1841</p>
                <div className="term-actions">
                  <button type="button" className="chip chip-accent" aria-expanded={review} onClick={() => setReview((r) => !r)}>
                    Review rollback
                  </button>
                  <button type="button" className="chip" onClick={() => { setReview(false); setRun((r) => r + 1); }}>
                    Replay analysis
                  </button>
                </div>
                {review ? (
                  <div className="term-review">
                    <p className="term-h">ROLLBACK PLAN · REQUIRES APPROVAL</p>
                    <ol>
                      <li><StatusDot tone="ok" /> locate-release · #1841 present on web-prod-04</li>
                      <li><StatusDot tone="idle" /> activate-release · pending approval</li>
                      <li><StatusDot tone="idle" /> HEALTH_CHECK · pending</li>
                      <li><StatusDot tone="idle" /> record · audit entry on completion</li>
                    </ol>
                    <p className="term-note">Approval request created for <span className="t-accent">infrastructure-admin</span>. Nothing runs until a human approves.</p>
                  </div>
                ) : null}
              </div>
            ) : null}
            {!done ? <span className="term-cursor" aria-hidden /> : null}
          </div>
        </div>
      </div>
    </section>
  );
}
