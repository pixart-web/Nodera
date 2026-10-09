import { cn } from "./cn";

export function Avatar({ initial, color = "#3B82F6", size = 36, className, round }: { initial: string; color?: string; size?: number; className?: string; round?: boolean }) {
  return (
    <span
      aria-hidden
      style={{ width: size, height: size, backgroundColor: color, fontSize: size * 0.42 }}
      className={cn("inline-flex shrink-0 items-center justify-center font-semibold text-white", round ? "rounded-full" : "rounded-nd", className)}
    >
      {initial}
    </span>
  );
}
