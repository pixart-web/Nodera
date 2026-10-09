"use client";

import type { ReactNode } from "react";
import { PageHeader } from "./PageHeader";
import { Card } from "./Card";
import { NotConnected } from "./Feedback";

// Shared frame for the simple "header + demo notice + table card" pages.
export function ListPage({ title, description, actions, children }: { title: string; description: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <div>
      <PageHeader title={title} description={description} actions={actions} />
      <NotConnected />
      <Card className="mt-5 p-5">{children}</Card>
    </div>
  );
}
