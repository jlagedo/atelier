import { ChatCircle, FolderOpen } from "@phosphor-icons/react";

import { cn } from "@/lib/utils";
import type { SessionMode } from "@/lib/mock-data";

const modes: Array<{ mode: SessionMode; label: string; icon: typeof ChatCircle }> = [
  { mode: "chat", label: "Chat", icon: ChatCircle },
  { mode: "work", label: "Work", icon: FolderOpen },
];

export function ModeSwitcher({
  activeMode,
  onModeChange,
}: {
  activeMode: SessionMode;
  onModeChange: (mode: SessionMode) => void;
}) {
  return (
    <div className="bg-sidebar-accent/60 grid grid-cols-2 gap-1 rounded-lg p-1">
      {modes.map(({ mode, label, icon: Icon }) => (
        <button
          key={mode}
          type="button"
          onClick={() => onModeChange(mode)}
          aria-pressed={activeMode === mode}
          className={cn(
            "text-muted-foreground flex h-8 items-center justify-center gap-1.5 rounded-md text-xs font-medium transition-colors",
            activeMode === mode && "bg-background text-foreground shadow-xs",
          )}
        >
          <Icon weight={activeMode === mode ? "fill" : "regular"} className="size-3.5" />
          <span>{label}</span>
        </button>
      ))}
    </div>
  );
}
