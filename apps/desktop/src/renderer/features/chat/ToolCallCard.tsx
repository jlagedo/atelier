import { useState } from "react";
import {
  CaretRight,
  CheckCircle,
  CircleNotch,
  FileText,
  Terminal,
  Wrench,
  XCircle,
  type Icon,
} from "@phosphor-icons/react";

import { cn } from "@/lib/utils";
import type { ToolCall } from "@/lib/mock-data";

function toolIcon(label: string): Icon {
  if (label.includes("python") || label.includes("ran")) return Terminal;
  if (label.includes("read") || label.includes("file")) return FileText;
  return Wrench;
}

function StatusDot({ status }: { status: ToolCall["status"] }) {
  if (status === "running")
    return <CircleNotch weight="bold" className="text-primary size-3.5 animate-spin" />;
  if (status === "error") return <XCircle weight="fill" className="text-destructive size-3.5" />;
  return <CheckCircle weight="fill" className="text-muted-foreground size-3.5" />;
}

export function ToolCallCard({ tool }: { tool: ToolCall }) {
  const [open, setOpen] = useState(true);
  const ToolIcon = toolIcon(tool.label);

  return (
    <div className="border-border bg-card/60 ml-10 overflow-hidden rounded-lg border">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="hover:bg-accent/40 flex w-full items-center gap-2 px-3 py-2 text-left transition-colors"
      >
        <CaretRight
          weight="bold"
          className={cn("text-muted-foreground size-3 transition-transform", open && "rotate-90")}
        />
        <ToolIcon weight="bold" className="text-muted-foreground size-4" />
        <span className="text-foreground text-xs font-medium">{tool.label}</span>
        <span className="text-muted-foreground/60">·</span>
        <span className="text-muted-foreground truncate font-mono text-xs">{tool.target}</span>
        <span className="ml-auto pl-2">
          <StatusDot status={tool.status} />
        </span>
      </button>
      {open && (
        <pre className="text-muted-foreground border-border/60 max-h-48 overflow-auto border-t px-3 py-2.5 font-mono text-xs leading-relaxed whitespace-pre-wrap">
          {tool.output}
        </pre>
      )}
    </div>
  );
}
