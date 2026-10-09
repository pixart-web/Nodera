export function Logo({ className }: { className?: string }) {
  return (
    <span className={`inline-flex items-center gap-2.5 ${className ?? ""}`}>
      <svg width="30" height="30" viewBox="0 0 32 32" aria-hidden>
        <defs>
          <linearGradient id="nlogo" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#38BDF8" /><stop offset="100%" stopColor="#6366F1" />
          </linearGradient>
        </defs>
        <path d="M5 27V5h5.2l11.6 14.6V5H27v22h-5.2L10.2 12.4V27z" fill="url(#nlogo)" />
      </svg>
      <span className="text-[22px] font-semibold tracking-tight text-nd-text">Nodera</span>
    </span>
  );
}
