"use client";

import { useEffect, useRef, useState, type RefObject } from "react";

/** Live `prefers-reduced-motion: reduce`. False during SSR and the first paint. */
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(false);
  useEffect(() => {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReduced(mq.matches);
    update();
    mq.addEventListener("change", update);
    return () => mq.removeEventListener("change", update);
  }, []);
  return reduced;
}

/**
 * True once the element enters the viewport (or, with `once: false`, while it
 * is in it). Sections use this to start their sequences and to pause CSS
 * animation when offscreen.
 */
export function useInView<T extends Element>(
  opts: { once?: boolean; rootMargin?: string; threshold?: number } = {},
): [RefObject<T | null>, boolean] {
  const { once = true, rootMargin = "0px 0px -15% 0px", threshold = 0 } = opts;
  const ref = useRef<T | null>(null);
  const [inView, setInView] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (typeof IntersectionObserver === "undefined") { setInView(true); return; }
    const io = new IntersectionObserver(([entry]) => {
      if (!entry) return;
      if (entry.isIntersecting) {
        setInView(true);
        if (once) io.disconnect();
      } else if (!once) {
        setInView(false);
      }
    }, { rootMargin, threshold });
    io.observe(el);
    return () => io.disconnect();
  }, [once, rootMargin, threshold]);
  return [ref, inView];
}

export const clamp01 = (n: number) => Math.min(1, Math.max(0, n));

/**
 * Writes scroll progress through a tall "pinned" section to the CSS custom
 * property `--p` (0 → 1) on that element, so the choreography itself lives in
 * CSS transforms/opacity. Only listens while the section is near the
 * viewport; one rAF per frame at most; no React re-render per scroll tick.
 * Reduced motion pins progress at 1 (the resolved, final state).
 */
export function useStickyProgress<T extends HTMLElement>(
  ref: RefObject<T | null>,
  onStep?: (p: number) => void,
) {
  const reduced = useReducedMotion();
  const cb = useRef(onStep);
  useEffect(() => { cb.current = onStep; }, [onStep]);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (reduced) {
      el.style.setProperty("--p", "1");
      cb.current?.(1);
      return;
    }
    let frame = 0;
    let active = false;
    const measure = () => {
      frame = 0;
      const r = el.getBoundingClientRect();
      const travel = r.height - window.innerHeight;
      const p = travel > 0 ? clamp01(-r.top / travel) : r.top < window.innerHeight * 0.5 ? 1 : 0;
      el.style.setProperty("--p", p.toFixed(4));
      cb.current?.(p);
    };
    const onScroll = () => { if (active && !frame) frame = requestAnimationFrame(measure); };
    const io = new IntersectionObserver(([entry]) => {
      active = !!entry?.isIntersecting;
      if (active) onScroll();
    }, { rootMargin: "20% 0px" });
    io.observe(el);
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    measure();
    return () => {
      io.disconnect();
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [ref, reduced]);
}

/** Interpolates 0 → target once `active` turns true. Reduced motion jumps straight to the value. */
export function useCountUp(target: number, active: boolean, decimals = 0, duration = 1400): string {
  const reduced = useReducedMotion();
  const [value, setValue] = useState(0);
  useEffect(() => {
    if (!active) return;
    if (reduced) { setValue(target); return; }
    let raf = 0;
    const start = performance.now();
    const tick = (now: number) => {
      const t = clamp01((now - start) / duration);
      const eased = 1 - Math.pow(1 - t, 4);
      setValue(target * eased);
      if (t < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [active, target, decimals, duration, reduced]);
  return value.toFixed(decimals);
}

/**
 * Steps an index 0 → count on a fixed cadence once `active` is true — the
 * engine behind the AI reasoning, incident timeline and security sequences.
 * Reduced motion resolves to the final step immediately.
 */
export function useSequence(count: number, active: boolean, stepMs: number, runKey = 0): number {
  const reduced = useReducedMotion();
  const [step, setStep] = useState(0);
  useEffect(() => {
    if (!active) return;
    if (reduced) { setStep(count); return; }
    setStep(0);
    let i = 0;
    const id = window.setInterval(() => {
      i += 1;
      setStep(i);
      if (i >= count) window.clearInterval(id);
    }, stepMs);
    return () => window.clearInterval(id);
  }, [active, count, stepMs, reduced, runKey]);
  return step;
}
