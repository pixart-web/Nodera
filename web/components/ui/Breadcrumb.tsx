import Link from "next/link";
import { ChevronRight } from "lucide-react";

export function Breadcrumb({ items }: { items: Array<{ label: string; href?: string }> }) {
  return (
    <nav aria-label="Breadcrumb" className="mb-3">
      <ol className="flex items-center gap-1.5 text-xs text-nd-muted">
        {items.map((it, i) => (
          <li key={it.label} className="flex items-center gap-1.5">
            {i > 0 && <ChevronRight className="h-3 w-3 text-nd-faint" aria-hidden />}
            {it.href ? <Link href={it.href} className="hover:text-nd-text">{it.label}</Link> : <span aria-current="page" className="text-nd-text">{it.label}</span>}
          </li>
        ))}
      </ol>
    </nav>
  );
}
