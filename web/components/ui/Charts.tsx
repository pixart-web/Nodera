import { useId } from "react";
import { cn } from "./cn";

function path(points: number[], w: number, h: number, max: number, pad = 2) {
  const n = points.length;
  if (n < 2) return { line: "", area: "" };
  const step = w / (n - 1);
  const ys = points.map((p) => h - pad - (Math.min(p, max) / max) * (h - pad * 2));
  const line = ys.map((y, i) => `${i === 0 ? "M" : "L"}${(i * step).toFixed(1)},${y.toFixed(1)}`).join(" ");
  return { line, area: `${line} L${w},${h} L0,${h} Z` };
}

export function Sparkline({ points, color = "rgb(var(--color-success))", className, label, max }: {
  points: number[]; color?: string; className?: string; label: string; max?: number;
}) {
  const id = useId();
  const m = max ?? Math.max(...points, 1) * 1.15;
  const { line, area } = path(points, 200, 48, m);
  return (
    <svg role="img" aria-label={label} viewBox="0 0 200 48" preserveAspectRatio="none" className={cn("h-12 w-full", className)}>
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.35" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${id})`} />
      <path d={line} fill="none" stroke={color} strokeWidth="1.8" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
    </svg>
  );
}

// Larger chart with gridlines, used on the Monitoring page.
export function AreaChart({ series, color = "rgb(var(--color-primary-soft))", label, unit = "%", max = 100, height = 160 }: {
  series: number[]; color?: string; label: string; unit?: string; max?: number; height?: number;
}) {
  const id = useId();
  const { line, area } = path(series, 400, height, max, 4);
  return (
    <div>
      <svg role="img" aria-label={label} viewBox={`0 0 400 ${height}`} preserveAspectRatio="none" className="w-full" style={{ height }}>
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity="0.3" /><stop offset="100%" stopColor={color} stopOpacity="0" />
          </linearGradient>
        </defs>
        {[0.25, 0.5, 0.75].map((g) => <line key={g} x1="0" x2="400" y1={height * g} y2={height * g} stroke="rgb(var(--color-border))" strokeDasharray="3 4" vectorEffect="non-scaling-stroke" />)}
        <path d={area} fill={`url(#${id})`} />
        <path d={line} fill="none" stroke={color} strokeWidth="2" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
      </svg>
      <div className="mt-1 flex justify-between text-[11px] text-nd-faint"><span>0{unit}</span><span>{max}{unit}</span></div>
    </div>
  );
}

export function ProgressBar({ value, tone, label }: { value: number; tone?: "auto" | "primary"; label: string }) {
  const v = Math.max(0, Math.min(100, value));
  const color = tone === "primary" ? "bg-nd-primary" : v > 85 ? "bg-nd-danger" : v > 70 ? "bg-nd-warning" : "bg-nd-success";
  return (
    <div role="progressbar" aria-label={label} aria-valuenow={Math.round(v)} aria-valuemin={0} aria-valuemax={100} className="h-1.5 w-full overflow-hidden rounded-full bg-nd-hover">
      <div className={cn("h-full rounded-full transition-[width] duration-500", color)} style={{ width: `${v}%` }} />
    </div>
  );
}

export function ProgressCircle({ value, size = 56, label }: { value: number; size?: number; label: string }) {
  const r = (size - 8) / 2, c = 2 * Math.PI * r, v = Math.max(0, Math.min(100, value));
  return (
    <div role="progressbar" aria-label={label} aria-valuenow={Math.round(v)} aria-valuemin={0} aria-valuemax={100} className="relative inline-flex" style={{ width: size, height: size }}>
      <svg width={size} height={size} className="-rotate-90">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="rgb(var(--color-border))" strokeWidth="4" />
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="rgb(var(--color-primary))" strokeWidth="4" strokeLinecap="round" strokeDasharray={c} strokeDashoffset={c * (1 - v / 100)} className="transition-[stroke-dashoffset] duration-500" />
      </svg>
      <span className="absolute inset-0 flex items-center justify-center text-xs font-semibold text-nd-text">{Math.round(v)}%</span>
    </div>
  );
}
