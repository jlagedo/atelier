import { Circle, CircleNotch, WarningCircle } from "@phosphor-icons/react";

import { cn } from "@/lib/utils";
import type { SessionStatus } from "@/lib/mock-data";

const labelByStatus: Record<SessionStatus, string> = {
  idle: "idle",
  running: "running",
  waiting: "waiting",
  done: "done",
  error: "error",
};

export function StatusBadge({ status, compact = false }: { status: SessionStatus; compact?: boolean }) {
  const Icon = status === "running" ? CircleNotch : status === "error" ? WarningCircle : Circle;

  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium",
        status === "running" && "bg-primary/15 text-primary",
        status === "waiting" && "bg-muted text-muted-foreground",
        status === "done" && "bg-sidebar-accent text-muted-foreground",
        status === "idle" && "bg-transparent text-muted-foreground",
        status === "error" && "bg-destructive/15 text-destructive",
      )}
    >
      <Icon className={cn("size-2.5", status === "running" && "animate-spin")} weight="fill" />
      {!compact && labelByStatus[status]}
    </span>
  );
}
