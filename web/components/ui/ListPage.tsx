"use client";

import type { ReactNode } from "react";
import { PageHeader } from "./PageHeader";
import { Card } from "./Card";

// Shared frame for the simple "header + table card" pages.
export function ListPage({ title, description, actions, children }: { title: string; description: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <div>
      <PageHeader title={title} description={description} actions={actions} />
      <Card className="mt-5 p-5">{children}</Card>
    </div>
  );
}
