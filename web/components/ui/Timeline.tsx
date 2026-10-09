import type { ReactNode } from "react";
import { STATUS } from "@/lib/status";
import type { ResourceStatus } from "@/lib/domain";
import { cn } from "./cn";

export interface TimelineItem { id: string; icon: ReactNode; status: ResourceStatus; title: string; description: string; time: string }

export function Timeline({ items }: { items: TimelineItem[] }) {
  return (
    <ol className="relative">
      {items.map((it, i) => {
        const t = STATUS[it.status];
        return (
          <li key={it.id} className="relative flex gap-3 pb-5 last:pb-0">
            {i < items.length - 1 && <span aria-hidden className="absolute left-[15px] top-8 h-[calc(100%-1.5rem)] w-px bg-nd-border" />}
            <span aria-hidden className={cn("relative z-10 flex h-8 w-8 shrink-0 items-center justify-center rounded-full border [&>svg]:h-4 [&>svg]:w-4", t.bg, t.ring, t.text)}>{it.icon}</span>
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline justify-between gap-2">
                <p className="truncate text-sm font-medium text-nd-text">{it.title}</p>
                <time className="shrink-0 text-xs text-nd-faint">{it.time}</time>
              </div>
              <p className="truncate text-xs text-nd-muted">{it.description}</p>
            </div>
          </li>
        );
      })}
    </ol>
  );
}
