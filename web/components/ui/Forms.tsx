"use client";

import { forwardRef, useId, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes } from "react";
import { Search } from "lucide-react";
import { cn } from "./cn";

const FIELD = "w-full rounded-nd border border-nd-strong bg-nd-elevated px-3 text-sm text-nd-text placeholder:text-nd-faint transition-colors duration-150 hover:border-nd-faint focus:border-nd-primary focus:outline-none focus:ring-1 focus:ring-nd-primary disabled:opacity-50";

export function Field({ label, hint, children }: { label: string; hint?: string; children: (id: string) => ReactNode }) {
  const id = useId();
  return (
    <div>
      <label htmlFor={id} className="mb-1.5 block text-xs font-medium text-nd-muted">{label}</label>
      {children(id)}
      {hint && <p className="mt-1 text-xs text-nd-faint">{hint}</p>}
    </div>
  );
}

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input({ className, ...rest }, ref) {
  return <input ref={ref} className={cn(FIELD, "h-9", className)} {...rest} />;
});

export function SearchInput({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <div className={cn("relative", className)}>
      <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-nd-faint" aria-hidden />
      <input type="search" className={cn(FIELD, "h-9 pl-9")} {...rest} />
    </div>
  );
}

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select className={cn(FIELD, "h-9 pr-8", className)} {...rest}>{children}</select>;
}

export function Checkbox({ label, ...rest }: InputHTMLAttributes<HTMLInputElement> & { label: string }) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-2 text-sm text-nd-text">
      <input type="checkbox" className="h-4 w-4 rounded border-nd-strong bg-nd-elevated accent-[rgb(var(--color-primary))]" {...rest} />
      {label}
    </label>
  );
}

export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button type="button" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}
      className={cn("relative h-5 w-9 shrink-0 rounded-full transition-colors duration-200 disabled:opacity-50", checked ? "bg-nd-primary" : "bg-nd-strong")}>
      <span className={cn("absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all duration-200", checked ? "left-[18px]" : "left-0.5")} />
    </button>
  );
}
