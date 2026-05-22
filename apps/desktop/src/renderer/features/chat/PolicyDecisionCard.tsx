import { ShieldCheck, Warning } from "@phosphor-icons/react";

import type { PolicyDecision } from "@/lib/mock-data";

// Display-only (S6.1): there is no approval. Allowed actions show a quiet audited
// badge; denied actions show a clear warning — Atelier can't do X, blocked by the
// fixed policy, recorded in the audit log. No override.
export function PolicyDecisionCard({ policy }: { policy: PolicyDecision }) {
  const denied = policy.decision === "deny";

  if (!denied) {
    return (
      <div className="text-muted-foreground ml-10 flex items-center gap-1.5 px-1 text-[11px]">
        <ShieldCheck weight="bold" className="text-primary/70 size-3.5 shrink-0" />
        <span className="font-medium">{policy.action}</span>
        {policy.target && (
          <code className="bg-muted/60 truncate rounded px-1 py-0.5 font-mono text-[10px]">{policy.target}</code>
        )}
        <span>allowed by policy</span>
      </div>
    );
  }

  return (
    <div className="border-destructive/40 bg-destructive/[0.07] ml-10 rounded-lg border">
      <div className="flex items-start gap-3 px-4 py-3">
        <div className="bg-destructive/15 text-destructive mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md">
          <Warning weight="bold" className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-foreground text-sm">
            Atelier can&apos;t <span className="font-medium">{policy.action}</span>
            {policy.target && (
              <>
                {" "}
                <code className="bg-background/60 rounded px-1 py-0.5 font-mono text-xs">{policy.target}</code>
              </>
            )}{" "}
            <span className="text-muted-foreground">— blocked by policy.</span>
          </p>
          <p className="text-muted-foreground mt-1 text-xs">
            {policy.reason} · recorded in the audit log.
          </p>
        </div>
      </div>
    </div>
  );
}
