const COLORS: Record<string, string> = {
  // infra / application statuses
  online: "bg-ok/15 text-ok",
  running: "bg-ok/15 text-ok",
  succeeded: "bg-ok/15 text-ok",
  available: "bg-ok/15 text-ok",
  active: "bg-ok/15 text-ok",
  degraded: "bg-warn/15 text-warn",
  retrying: "bg-warn/15 text-warn",
  queued: "bg-base-500/20 text-base-200",
  unknown: "bg-base-500/20 text-base-300",
  stopped: "bg-base-500/20 text-base-300",
  cancelled: "bg-base-500/20 text-base-300",
  offline: "bg-danger/15 text-danger",
  failed: "bg-danger/15 text-danger",
  unavailable: "bg-danger/15 text-danger",
  disabled: "bg-danger/15 text-danger",
  // approval statuses
  pending: "bg-warn/15 text-warn",
  approved: "bg-ok/15 text-ok",
  rejected: "bg-danger/15 text-danger",
  expired: "bg-base-500/20 text-base-300",
};

export function StatusBadge({ status }: { status: string }) {
  const cls = COLORS[status] ?? "bg-base-500/20 text-base-300";
  return <span className={`badge ${cls}`}>{status}</span>;
}
