"use client";

import { MoreVertical } from "lucide-react";
import { Dropdown, type MenuItem } from "./Dropdown";

export function RowMenu({ label, items }: { label: string; items: MenuItem[] }) {
  return (
    <Dropdown label={label} items={items}
      trigger={<span className="flex h-8 w-8 items-center justify-center rounded-md text-nd-muted transition-colors hover:bg-nd-hover hover:text-nd-text"><MoreVertical className="h-4 w-4" /></span>} />
  );
}
