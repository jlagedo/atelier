import { Circle, CircleNotch, Moon, WarningCircle } from "@phosphor-icons/react";
import type { VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";
import { badgeVariants } from "@/components/ui/badge";
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

type BadgeTone = NonNullable<VariantProps<typeof badgeVariants>["tone"]>;

const toneByStatus: Record<SessionStatus, BadgeTone> = {
  idle: "plain",
  running: "accent",
  waiting: "muted",
  done: "subtle",
  error: "destructive",
  starting: "accent",
  active: "accent",
  resuming: "accent",
  hibernating: "muted",
  inactive: "plain",
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
    <span className={badgeVariants({ tone: toneByStatus[status] })}>
      <Icon className={cn("size-2.5", SPINNING.has(status) && "animate-spin")} weight="fill" />
      {!compact && labelByStatus[status]}
    </span>
  );
}
