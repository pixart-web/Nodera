"use client";

import { NoderaMark, NoderaWordmark } from "@/components/brand/NoderaMark";
import { siteConfig, siteLinks } from "@/lib/site-config";
import { useInView } from "./hooks";
import { PrimaryCta, TechLink } from "./primitives";

const INCLUDED = [
  "Infrastructure visibility",
  "Server management",
  "Deployments",
  "Docker operations",
  "Monitoring",
  "Backups",
  "Audit & security",
  "AI infrastructure intelligence",
];

const COSTS = ["One incident", "One failed restore", "Hours of manual server work", "A stack of overlapping tools"];

export function Pricing() {
  const [ref, inView] = useInView<HTMLElement>();
  return (
    <section ref={ref} id="pricing" className="layer pricing" data-inview={inView} aria-labelledby="pricing-title" data-layer="09" data-layer-name="Pricing">
      <div className="container pricing-grid">
        <div className="pricing-copy">
          <p className="label layer-tag"><span className="layer-n">PRICING</span><span className="layer-rule" aria-hidden />ONE PLAN</p>
          <h2 id="pricing-title" className="h2">One control plane. {siteConfig.price.display}/month.</h2>
          <p className="lede">Everything your infrastructure team needs to operate with more visibility, control and confidence.</p>
          <div className="cost-compare">
            <p className="label">Measured against</p>
            <ul>
              {COSTS.map((c) => <li key={c}>{c}</li>)}
            </ul>
            <p className="cost-note">An hour of downtime, a backup that never restores, or a week of manual operations can easily cost more than a year of Nodera. You know your own numbers better than anyone.</p>
          </div>
        </div>

        <div className="price-card">
          <div className="price-scan" aria-hidden />
          <p className="label">Nodera · all included</p>
          <p className="price">
            <span className="price-v">{siteConfig.price.display}</span>
            <span className="price-p">{siteConfig.price.period}</span>
          </p>
          <ul className="included">
            {INCLUDED.map((f) => (
              <li key={f}>
                <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden focusable="false"><path d="M2 7.5l3 3 7-7" fill="none" stroke="currentColor" strokeWidth="1.6" /></svg>
                {f}
              </li>
            ))}
          </ul>
          <PrimaryCta href={siteLinks.start} className="btn-block">Start with Nodera</PrimaryCta>
          <p className="price-meta mono">One infrastructure control layer. No tiers. No per-seat maths.</p>
        </div>
      </div>
    </section>
  );
}

// Scattered topology nodes converge onto the cells of the NODERA mark.
const TARGETS: [number, number][] = [];
// rack units (left column, three units) + diagonal + right column, sampled on a 48-unit grid
for (const [x, y0, y1] of [[11.5, 9.5, 15.5], [11.5, 21, 27], [11.5, 32.5, 38.5]] as const) for (let y = y0; y <= y1; y += 3) TARGETS.push([x, y]);
for (let t = 0; t <= 1.0001; t += 1 / 9) TARGETS.push([20.5 + 7 * t, 9.5 + 29 * t]);
for (let y = 9.5; y <= 38.5; y += 3.2) TARGETS.push([36.5, y]);

function scatter(i: number): [number, number] {
  // deterministic pseudo-random spread (no Math.random → no hydration mismatch)
  const a = Math.sin(i * 12.9898) * 43758.5453;
  const b = Math.sin(i * 78.233) * 12543.123;
  return [((a - Math.floor(a)) * 2 - 1) * 70 + 24, ((b - Math.floor(b)) * 2 - 1) * 34 + 24];
}

export function FinalCta() {
  const [ref, inView] = useInView<HTMLElement>({ rootMargin: "0px 0px -30% 0px" });
  return (
    <section ref={ref} className="final" data-inview={inView} aria-labelledby="final-title" data-layer="10" data-layer-name="Control">
      <div className="final-visual" aria-hidden>
        <svg viewBox="-60 -20 168 88" focusable="false">
          {TARGETS.map(([x, y], i) => {
            const [sx, sy] = scatter(i + 1);
            return (
              <rect
                key={i}
                className={`fnode ${i < 3 ? "is-live" : ""}`}
                x={x - 1}
                y={y - 1}
                width="2"
                height="2"
                style={{ ["--sx" as string]: `${(sx - x).toFixed(2)}px`, ["--sy" as string]: `${(sy - y).toFixed(2)}px`, ["--i" as string]: i }}
              />
            );
          })}
          <g className="final-mark">
            <rect x="8" y="8" width="7" height="9" className="live" />
            <rect x="8" y="19.5" width="7" height="9" /><rect x="8" y="31" width="7" height="9" />
            <path d="M17.5 8h6l7 32h-6z" /><rect x="33" y="8" width="7" height="32" />
          </g>
        </svg>
      </div>
      <div className="container final-copy">
        <h2 id="final-title" className="h1 final-title">Take control of your infrastructure.</h2>
        <p className="lede">See what Nodera can do for your infrastructure.</p>
        <div className="hero-ctas">
          <PrimaryCta href={siteLinks.start}>Start with Nodera</PrimaryCta>
          <TechLink href={siteLinks.talk}>Talk to our team</TechLink>
        </div>
      </div>
    </section>
  );
}

export function SiteFooter() {
  return (
    <footer className="footer" id="contact">
      <div className="container footer-grid">
        <div className="footer-brand">
          <span className="footer-lockup"><NoderaMark size={28} /><NoderaWordmark height={11} /></span>
          <p>Infrastructure, under control.</p>
        </div>
        <nav aria-label="Footer" className="footer-nav">
          <a href="#platform">Platform</a>
          <a href="#intelligence">Intelligence</a>
          <a href="#security">Security</a>
          <a href="#pricing">Pricing</a>
          <a href={siteLinks.talk === "#contact" ? "#contact-details" : siteLinks.talk}>Contact</a>
        </nav>
        <div className="footer-contact" id="contact-details">
          <p className="label">Contact</p>
          {siteConfig.contactEmail ? (
            <a href={`mailto:${siteConfig.contactEmail}`} className="mono">{siteConfig.contactEmail}</a>
          ) : (
            <p className="placeholder mono">[contact address — to be published]</p>
          )}
        </div>
      </div>
      <div className="container footer-legal">
        <p className="mono">© {new Date().getFullYear()} Nodera</p>
        <div>
          <a id="legal-privacy" href={siteLinks.privacy} aria-describedby="legal-note">Privacy</a>
          <a id="legal-terms" href={siteLinks.terms} aria-describedby="legal-note">Terms</a>
          <span id="legal-note" className="placeholder mono">[legal pages — placeholders]</span>
        </div>
      </div>
    </footer>
  );
}
