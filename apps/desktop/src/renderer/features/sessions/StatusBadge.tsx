import { Circle, CircleNotch, Moon, WarningCircle } from "@phosphor-icons/react";

import { cn } from "@/lib/utils";
import type { SessionStatus } from "@/lib/mock-data";

const labelByStatus: Record<SessionStatus, string> = {
  idle: "idle",
  running: "running",
  waiting: "waiting",
  done: "done",
  error: "error",
  starting: "starting",
  active: "active",
  resuming: "resuming",
  hibernating: "sleeping",
  inactive: "dormant",
};

const SPINNING = new Set<SessionStatus>(["running", "starting", "resuming", "hibernating"]);

function iconFor(status: SessionStatus) {
  if (SPINNING.has(status)) return CircleNotch;
  if (status === "error") return WarningCircle;
  if (status === "inactive") return Moon;
  return Circle;
}

export function StatusBadge({ status, compact = false }: { status: SessionStatus; compact?: boolean }) {
  const Icon = iconFor(status);

  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium",
        (status === "running" || status === "starting" || status === "resuming" || status === "active") &&
          "bg-primary/15 text-primary",
        (status === "waiting" || status === "hibernating") && "bg-muted text-muted-foreground",
        status === "done" && "bg-sidebar-accent text-muted-foreground",
        (status === "idle" || status === "inactive") && "bg-transparent text-muted-foreground",
        status === "error" && "bg-destructive/15 text-destructive",
      )}
    >
      <Icon className={cn("size-2.5", SPINNING.has(status) && "animate-spin")} weight="fill" />
      {!compact && labelByStatus[status]}
    </span>
  );
}
