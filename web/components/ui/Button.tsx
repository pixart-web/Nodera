import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cn } from "./cn";

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md";

const VARIANT: Record<Variant, string> = {
  primary: "bg-nd-primary text-white hover:bg-nd-primary/90 shadow-[0_0_0_1px_rgb(255_255_255/0.06)_inset]",
  secondary: "border border-nd-strong bg-nd-surface text-nd-text hover:bg-nd-hover",
  ghost: "text-nd-muted hover:bg-nd-hover hover:text-nd-text",
  danger: "border border-nd-danger/30 bg-nd-danger/10 text-nd-danger hover:bg-nd-danger/20",
};
const SIZE: Record<Size, string> = { sm: "h-8 px-3 text-xs", md: "h-9 px-4 text-sm" };

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant; size?: Size; icon?: ReactNode;
}

export function Button({ variant = "secondary", size = "md", icon, className, children, type = "button", ...rest }: ButtonProps) {
  return (
    <button
      type={type}
      className={cn("inline-flex items-center justify-center gap-2 rounded-nd font-medium transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50", VARIANT[variant], SIZE[size], className)}
      {...rest}
    >
      {icon}
      {children}
    </button>
  );
}

export function IconButton({ label, className, children, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      className={cn("inline-flex h-9 w-9 items-center justify-center rounded-nd text-nd-muted transition-colors duration-150 hover:bg-nd-hover hover:text-nd-text", className)}
      {...rest}
    >
      {children}
    </button>
  );
}
