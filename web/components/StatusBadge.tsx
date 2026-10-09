// Maps the API's raw status strings onto the Nodera status tokens
// (lib/status.ts) so API-backed pages use the same colours as the rest.
import { STATUS } from "@/lib/status";
import type { ResourceStatus } from "@/lib/domain";
import { StatusDot } from "@/components/ui/Status";

const MAP: Record<string, ResourceStatus> = {
  online: "ONLINE", running: "ONLINE", succeeded: "ONLINE", available: "ONLINE", active: "ONLINE", approved: "ONLINE", executed: "ONLINE",
  degraded: "WARNING", retrying: "WARNING", pending: "WARNING", deprecated: "WARNING",
  executing: "DEPLOYING",
  queued: "PENDING",
  unknown: "UNKNOWN", stopped: "UNKNOWN", cancelled: "UNKNOWN", expired: "UNKNOWN", decommissioned: "UNKNOWN", deregistered: "UNKNOWN",
  offline: "OFFLINE", failed: "ERROR", unavailable: "OFFLINE", disabled: "OFFLINE", rejected: "ERROR", execution_failed: "ERROR",
};

export function StatusBadge({ status }: { status: string }) {
  const key = MAP[status] ?? "UNKNOWN";
  const t = STATUS[key];
  return (
    <span className={`badge ${t.text} ${t.bg} ${t.ring}`}>
      <StatusDot status={key} />
      {status}
    </span>
  );
}
