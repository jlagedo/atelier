import { Cpu, FolderOpen, ShieldCheck } from "@phosphor-icons/react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { ThemeToggle } from "@/components/theme-toggle";
import type { Session } from "@/lib/mock-data";
import { StatusBadge } from "@/features/sessions/StatusBadge";

export function ChatHeader({
  session,
  workspaceOpen,
  onToggleWorkspace,
}: {
  session: Session;
  workspaceOpen: boolean;
  onToggleWorkspace: () => void;
}) {
  const isWork = session.mode === "work";
  const subtitle = isWork
    ? `${session.folderPath} · Sandbox VM · writes gated`
    : "Chat session · no work folder";

  return (
    <header className="border-border bg-background/70 flex h-header shrink-0 items-center gap-2 border-b px-5 backdrop-blur">
      <SidebarTrigger className="text-muted-foreground" />
      <Separator orientation="vertical" className="mr-1 h-5" />
      <div className="flex min-w-0 flex-col">
        <div className="flex min-w-0 items-center gap-2">
          <h1 className="text-foreground truncate text-sm font-medium">{session.title}</h1>
          <Badge tone="accent">{isWork ? "Work" : "Chat"}</Badge>
          {isWork && <StatusBadge status={session.status} />}
        </div>
        <div className="text-muted-foreground flex items-center gap-2 text-[11px]">
          {isWork ? (
            <>
              <span className="max-w-[34rem] truncate font-mono">{subtitle}</span>
              <span className="hidden items-center gap-1 lg:flex">
                <ShieldCheck className="size-3" /> Contained
              </span>
            </>
          ) : (
            <>
              <span>{subtitle}</span>
              <span className="flex items-center gap-1">
                <Cpu className="size-3" /> Ideas and planning
              </span>
            </>
          )}
        </div>
      </div>
      <div className="ml-auto flex items-center gap-1">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant={workspaceOpen ? "secondary" : "ghost"}
              size="icon"
              onClick={onToggleWorkspace}
              aria-label={isWork ? "Toggle work context panel" : "Toggle chat context panel"}
            >
              <FolderOpen weight={workspaceOpen ? "fill" : "regular"} />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{isWork ? "Work context" : "Chat context"}</TooltipContent>
        </Tooltip>
        <ThemeToggle />
      </div>
    </header>
  );
}
