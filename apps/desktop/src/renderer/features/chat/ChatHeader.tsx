import { Cpu, FolderOpen, ShieldCheck } from "@phosphor-icons/react";

import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { ThemeToggle } from "@/components/theme-toggle";

export function ChatHeader({
  title,
  workspaceOpen,
  onToggleWorkspace,
}: {
  title: string;
  workspaceOpen: boolean;
  onToggleWorkspace: () => void;
}) {
  return (
    <header className="border-border bg-background/80 flex h-14 shrink-0 items-center gap-2 border-b px-3 backdrop-blur">
      <SidebarTrigger className="text-muted-foreground" />
      <Separator orientation="vertical" className="mr-1 h-5" />
      <div className="flex min-w-0 flex-col">
        <h1 className="text-foreground truncate text-sm font-medium">{title}</h1>
        <div className="text-muted-foreground flex items-center gap-2 text-[11px]">
          <span className="flex items-center gap-1">
            <Cpu className="size-3" /> Sandbox VM
          </span>
          <span className="flex items-center gap-1">
            <ShieldCheck className="size-3" /> Contained
          </span>
        </div>
      </div>
      <div className="ml-auto flex items-center gap-1">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant={workspaceOpen ? "secondary" : "ghost"}
              size="icon"
              onClick={onToggleWorkspace}
              aria-label="Toggle workspace panel"
            >
              <FolderOpen weight={workspaceOpen ? "fill" : "regular"} />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">Workspace files</TooltipContent>
        </Tooltip>
        <ThemeToggle />
      </div>
    </header>
  );
}
