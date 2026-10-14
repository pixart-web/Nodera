"use client";

import { useEffect, useState } from "react";

/** Fixed rail (desktop) naming the operational layer currently in view. */
export function LayerIndicator() {
  const [layer, setLayer] = useState<{ n: string; name: string } | null>(null);

  useEffect(() => {
    const els = Array.from(document.querySelectorAll<HTMLElement>("[data-layer]"));
    if (!els.length || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (e.isIntersecting) {
          const el = e.target as HTMLElement;
          setLayer({ n: el.dataset.layer ?? "", name: el.dataset.layerName ?? "" });
        }
      }
    }, { rootMargin: "-50% 0px -50% 0px" });
    els.forEach((el) => io.observe(el));
    const top = new IntersectionObserver(([e]) => { if (e?.isIntersecting) setLayer(null); }, { rootMargin: "-50% 0px -50% 0px" });
    const hero = document.getElementById("top");
    if (hero) top.observe(hero);
    return () => { io.disconnect(); top.disconnect(); };
  }, []);

  return (
    <div className={`layer-rail ${layer ? "is-on" : ""}`} aria-hidden>
      <span className="rail-line" />
      <span className="mono rail-n">{layer ? `L${layer.n}` : ""}</span>
      <span className="rail-name">{layer?.name ?? ""}</span>
    </div>
  );
}
