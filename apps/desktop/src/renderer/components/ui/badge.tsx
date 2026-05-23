import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";

// The pill primitive used for inline status/labels (mode tag, file status,
// session status). One shape, a small set of tones — so the badge geometry
// lives here instead of being re-typed at every call site.
const badgeVariants = cva(
  "inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium",
  {
    variants: {
      tone: {
        accent: "bg-primary/15 text-primary",
        muted: "bg-muted text-muted-foreground",
        subtle: "bg-sidebar-accent text-muted-foreground",
        destructive: "bg-destructive/15 text-destructive",
        plain: "bg-transparent text-muted-foreground",
      },
    },
    defaultVariants: {
      tone: "muted",
    },
  },
);

function Badge({
  className,
  tone,
  ...props
}: React.ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
  return <span data-slot="badge" className={cn(badgeVariants({ tone, className }))} {...props} />;
}

export { Badge, badgeVariants };
