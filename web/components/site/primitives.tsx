import type { ReactNode } from "react";

function Arrow() {
  return (
    <svg className="arrow" width="14" height="14" viewBox="0 0 14 14" aria-hidden focusable="false">
      <path d="M1 7h11M8 3l4 4-4 4" fill="none" stroke="currentColor" strokeWidth="1.5" />
    </svg>
  );
}

export function PrimaryCta({ href, children, className }: { href: string; children: ReactNode; className?: string }) {
  return (
    <a href={href} className={["btn-primary", className].filter(Boolean).join(" ")}>
      <span>{children}</span>
      <Arrow />
    </a>
  );
}

export function TechLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a href={href} className="tech-link">
      <span>{children}</span>
      <Arrow />
    </a>
  );
}

/** "LAYER 03 — INTELLIGENCE" header used at the start of every operational layer. */
export function LayerHeader({ n, name, title, lede, id }: { n: string; name: string; title: ReactNode; lede?: ReactNode; id?: string }) {
  return (
    <header className="layer-head">
      <p className="label layer-tag"><span className="layer-n">LAYER {n}</span><span className="layer-rule" aria-hidden />{name}</p>
      <h2 id={id} className="h2">{title}</h2>
      {lede ? <p className="lede">{lede}</p> : null}
    </header>
  );
}

export function DemoTag({ children = "DEMO DATA" }: { children?: ReactNode }) {
  return <span className="demo-tag" title="Illustrative demonstration — not real customer data">{children}</span>;
}

/** Product window chrome for demo interfaces. */
export function Window({ title, children, className, label }: { title: string; children: ReactNode; className?: string; label?: string }) {
  return (
    <div className={["window", className].filter(Boolean).join(" ")} role="group" aria-label={label ?? `${title} (demonstration)`}>
      <div className="window-bar">
        <span className="window-dots" aria-hidden><i /><i /><i /></span>
        <span className="window-title mono">{title}</span>
        <DemoTag />
      </div>
      <div className="window-body">{children}</div>
    </div>
  );
}

export function StatusDot({ tone }: { tone: "ok" | "warn" | "bad" | "info" | "idle" }) {
  return <i className={`dot dot-${tone}`} aria-hidden />;
}
