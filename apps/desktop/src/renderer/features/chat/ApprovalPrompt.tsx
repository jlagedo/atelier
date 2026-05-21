import { useState } from "react";
import { ShieldCheck, Check, X } from "@phosphor-icons/react";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import type { Approval } from "@/lib/mock-data";

export function ApprovalPrompt({ approval }: { approval: Approval }) {
  const [status, setStatus] = useState<Approval["status"]>(approval.status);

  return (
    <div className="border-primary/30 bg-primary/[0.06] ml-10 rounded-lg border">
      <div className="flex items-start gap-3 px-4 py-3">
        <div className="bg-primary/15 text-primary mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md">
          <ShieldCheck weight="bold" className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-foreground text-sm">
            <span className="font-medium">{approval.action}</span>{" "}
            <code className="bg-background/60 rounded px-1 py-0.5 font-mono text-xs">
              {approval.target}
            </code>{" "}
            <span className="text-muted-foreground">{approval.detail}?</span>
          </p>
          <p className="text-muted-foreground mt-1 text-xs">
            This action is gated by the broker and will be recorded in the audit log.
          </p>
        </div>
        {status === "pending" ? (
          <div className="flex shrink-0 gap-2">
            <Button size="sm" onClick={() => setStatus("approved")}>
              <Check weight="bold" />
              Approve
            </Button>
            <Button size="sm" variant="outline" onClick={() => setStatus("denied")}>
              Deny
            </Button>
          </div>
        ) : (
          <div
            className={cn(
              "flex shrink-0 items-center gap-1.5 text-xs font-medium",
              status === "approved" ? "text-primary" : "text-muted-foreground",
            )}
          >
            {status === "approved" ? <Check weight="bold" /> : <X weight="bold" />}
            {status === "approved" ? "Approved" : "Denied"}
          </div>
        )}
      </div>
    </div>
  );
}
