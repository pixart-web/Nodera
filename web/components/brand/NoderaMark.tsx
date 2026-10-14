// NODERA logo system. Source of truth for the geometry; the static files in
// public/brand/*.svg are exports of exactly these paths.
//
// The mark is an N built from infrastructure: a rack column of three units
// (the top one "live" in the accent colour), a routed diagonal that never
// touches either structure, and a solid column. Construction order for
// motion: units stack → column rises → route connects → N complete.

type Tone = "brand" | "mono" | "accent";

interface MarkProps {
  size?: number;
  tone?: Tone;
  /** Adds the construction-motion classes (styled by the marketing site CSS). */
  animated?: boolean;
  className?: string;
  title?: string;
}

const ACCENT = "#6DD6FF";
const WHITE = "#F4F7FA";

export function NoderaMark({ size = 32, tone = "brand", animated = false, className, title }: MarkProps) {
  const body = tone === "accent" ? ACCENT : tone === "mono" ? "currentColor" : WHITE;
  const live = tone === "brand" ? ACCENT : body;
  const a = (n: string) => (animated ? `nd-mark-${n}` : undefined);
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 48 48"
      className={[animated ? "nd-mark nd-mark--build" : "nd-mark", className].filter(Boolean).join(" ")}
      role={title ? "img" : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : true}
      focusable="false"
    >
      <rect className={a("u1")} x="8" y="8" width="7" height="9" fill={live} />
      <rect className={a("u2")} x="8" y="19.5" width="7" height="9" fill={body} />
      <rect className={a("u3")} x="8" y="31" width="7" height="9" fill={body} />
      <path className={a("route")} d="M17.5 8h6l7 32h-6z" fill={body} />
      <rect className={a("col")} x="33" y="8" width="7" height="32" fill={body} />
    </svg>
  );
}

/** Geometric NODERA wordmark (stroke-built, same stencil logic as the mark). */
export function NoderaWordmark({ height = 14, className, color = "currentColor" }: { height?: number; className?: string; color?: string }) {
  return (
    <svg
      height={height}
      width={(height * 152) / 24}
      viewBox="0 0 152 24"
      className={className}
      role="img"
      aria-label="NODERA"
      focusable="false"
    >
      <g fill="none" stroke={color} strokeWidth="3" strokeLinejoin="miter" strokeMiterlimit={10}>
        <path d="M0 .5h3v23H0zM13 .5h3v23h-3zM4.5 .5h3.5l3.5 23H8z" stroke="none" fill={color} />
        <path transform="translate(23 0)" d="M5.5 2h10l4 4v12l-4 4h-10l-4-4V6z" />
        <path transform="translate(51 0)" d="M1.5 2h13l5 5v10l-5 5h-13z" />
        <path transform="translate(79 0)" d="M17 2H1.5v20H17M1.5 12H14" />
        <path transform="translate(104.5 0)" d="M1.5 23.5V2h13l4 4v3l-4 4H3M11 13l7.5 10.5" />
        <path transform="translate(131.5 0)" d="M1.5 23.5V7l5-5h7l5 5v16.5M3 14h14" />
      </g>
    </svg>
  );
}

export function NoderaLockup({ size = 28, animated = false, className }: { size?: number; animated?: boolean; className?: string }) {
  return (
    <span className={["nd-lockup", className].filter(Boolean).join(" ")} style={{ display: "inline-flex", alignItems: "center", gap: size * 0.36 }}>
      <NoderaMark size={size} animated={animated} />
      <NoderaWordmark height={size * 0.38} />
    </span>
  );
}
