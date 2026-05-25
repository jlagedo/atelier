import { useEffect, useState } from "react";
import { FolderPlus, NotePencil } from "@phosphor-icons/react";

import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { ModeSwitcher } from "@/features/sessions/ModeSwitcher";
import { SessionList } from "@/features/sessions/SessionList";
import type { Session, SessionMode } from "@/lib/mock-data";

export function AppSidebar({
  activeMode,
  activeId,
  sessions,
  onModeChange,
  onSelect,
  onNewSession,
  onKill,
  onDelete,
}: {
  activeMode: SessionMode;
  activeId: string;
  sessions: Session[];
  onModeChange: (mode: SessionMode) => void;
  onSelect: (id: string) => void;
  onNewSession: () => void;
  onKill?: (id: string) => void;
  onDelete?: (id: string) => void;
}) {
  const [version, setVersion] = useState("…");
  const isWork = activeMode === "work";
  const ActionIcon = isWork ? FolderPlus : NotePencil;

  useEffect(() => {
    window.atelier
      ?.getVersion()
      .then(setVersion)
      .catch(() => setVersion("dev"));
  }, []);

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" className="hover:bg-transparent active:bg-transparent">
              <div className="bg-signal text-signal-foreground shadow-signal flex aspect-square size-8 items-center justify-center rounded-md">
                <span className="font-display text-lg leading-none font-semibold">A</span>
              </div>
              <div className="flex flex-col gap-0.5 leading-none">
                <span className="font-display text-base font-semibold tracking-tight">Atelier</span>
                <span className="text-muted-foreground text-[11px]">workspace</span>
              </div>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <div className="px-2 pt-2">
          <ModeSwitcher activeMode={activeMode} onModeChange={onModeChange} />
        </div>

        <div className="px-2 pt-1">
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip={isWork ? "New work" : "New chat"}
                onClick={onNewSession}
                className="text-primary hover:text-primary font-medium"
              >
                <ActionIcon weight="bold" />
                <span>{isWork ? "New work" : "New chat"}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </div>

        <SessionList
          mode={activeMode}
          sessions={sessions}
          activeId={activeId}
          onSelect={onSelect}
          onKill={onKill}
          onDelete={onDelete}
        />
      </SidebarContent>

      <SidebarFooter>
        <div className="flex items-center gap-2 px-1 py-1">
          <Avatar className="size-8">
            <AvatarFallback className="bg-secondary text-secondary-foreground text-xs font-medium">
              JL
            </AvatarFallback>
          </Avatar>
          <div className="flex min-w-0 flex-col leading-tight">
            <span className="text-sidebar-foreground truncate text-sm font-medium">
              João Lagedo
            </span>
            <span className="text-muted-foreground truncate text-[11px]">
              jlagedo@icloud.com · v{version}
            </span>
          </div>
        </div>
      </SidebarFooter>

      <SidebarRail />
    </Sidebar>
  );
}
