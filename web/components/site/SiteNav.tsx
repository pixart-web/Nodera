"use client";

import { useEffect, useRef, useState } from "react";
import { NoderaMark, NoderaWordmark } from "@/components/brand/NoderaMark";
import { siteLinks } from "@/lib/site-config";
import { PrimaryCta } from "./primitives";

const ITEMS = [
  { href: "#platform", label: "Platform" },
  { href: "#intelligence", label: "Intelligence" },
  { href: "#infrastructure", label: "Infrastructure" },
  { href: "#security", label: "Security" },
  { href: "#pricing", label: "Pricing" },
];

export function SiteNav() {
  const [open, setOpen] = useState(false);
  const [scrolled, setScrolled] = useState(false);
  const toggleRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 24);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    menuRef.current?.querySelector<HTMLElement>("a")?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { setOpen(false); toggleRef.current?.focus(); return; }
      if (e.key !== "Tab" || !menuRef.current) return;
      // Keep focus inside the full-screen menu while it is open.
      const f = Array.from(menuRef.current.querySelectorAll<HTMLElement>("a, button"));
      f.unshift(toggleRef.current as HTMLElement);
      const first = f[0];
      const last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last?.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first?.focus(); }
    };
    document.addEventListener("keydown", onKey);
    return () => { document.body.style.overflow = prev; document.removeEventListener("keydown", onKey); };
  }, [open]);

  useEffect(() => {
    const mq = window.matchMedia("(min-width: 960px)");
    const close = () => { if (mq.matches) setOpen(false); };
    mq.addEventListener("change", close);
    return () => mq.removeEventListener("change", close);
  }, []);

  return (
    <header className={`nav ${scrolled ? "is-scrolled" : ""} ${open ? "is-open" : ""}`}>
      <div className="nav-inner">
        <a href="#top" className="nav-brand" aria-label="NODERA — back to top">
          <NoderaMark size={26} animated />
          <NoderaWordmark height={11} className="nav-wordmark" />
        </a>
        <nav aria-label="Primary" className="nav-links">
          <ul>
            {ITEMS.map((it) => (
              <li key={it.href}><a href={it.href}>{it.label}</a></li>
            ))}
          </ul>
        </nav>
        <div className="nav-actions">
          <a href={siteLinks.signIn} className="nav-signin">Sign in</a>
          <PrimaryCta href={siteLinks.start} className="btn-sm">Start with Nodera</PrimaryCta>
        </div>
        <button
          ref={toggleRef}
          type="button"
          className="nav-toggle"
          aria-expanded={open}
          aria-controls="site-menu"
          aria-label={open ? "Close menu" : "Open menu"}
          onClick={() => setOpen((o) => !o)}
        >
          <span /><span />
        </button>
      </div>

      <div id="site-menu" ref={menuRef} className="menu" hidden={!open}>
        <p className="label menu-label">NAVIGATION · NODERA CONTROL LAYER</p>
        <ol className="menu-list">
          {ITEMS.map((it, i) => (
            <li key={it.href} style={{ ["--i" as string]: i }}>
              <a href={it.href} onClick={() => setOpen(false)}>
                <span className="mono menu-n">0{i + 1}</span>
                <span>{it.label}</span>
              </a>
            </li>
          ))}
        </ol>
        <div className="menu-foot">
          <PrimaryCta href={siteLinks.start}>Start with Nodera</PrimaryCta>
          <a href={siteLinks.signIn} className="tech-link"><span>Sign in</span></a>
          <p className="mono menu-meta">€99/month · one control plane</p>
        </div>
      </div>
    </header>
  );
}
