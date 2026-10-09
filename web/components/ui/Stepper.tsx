import { Check } from "lucide-react";
import { cn } from "./cn";

export function Stepper({ steps, current }: { steps: string[]; current: number }) {
  return (
    <ol className="flex items-center overflow-x-auto nd-scroll" aria-label="Progresso">
      {steps.map((s, i) => (
        <li key={s} aria-current={i === current ? "step" : undefined} className="flex flex-1 items-center last:flex-none">
          <div className="flex items-center gap-2.5">
            <span className={cn("flex h-7 w-7 shrink-0 items-center justify-center rounded-full border text-xs font-semibold transition-colors duration-200",
              i < current ? "border-nd-success bg-nd-success text-white" : i === current ? "border-nd-primary bg-nd-primary text-white" : "border-nd-strong text-nd-muted")}>
              {i < current ? <Check className="h-3.5 w-3.5" aria-hidden /> : i + 1}
            </span>
            <span className={cn("whitespace-nowrap text-sm", i === current ? "font-semibold text-nd-text" : "text-nd-muted")}>{s}</span>
          </div>
          {i < steps.length - 1 && <span aria-hidden className={cn("mx-3 h-px min-w-6 flex-1", i < current ? "bg-nd-success" : "bg-nd-border")} />}
        </li>
      ))}
    </ol>
  );
}
