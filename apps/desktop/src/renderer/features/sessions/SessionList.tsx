import { ChatCircle, FolderOpen } from "@phosphor-icons/react";

import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import type { Session, SessionMode } from "@/lib/mock-data";
import { StatusBadge } from "./StatusBadge";

export function SessionList({
  mode,
  sessions,
  activeId,
  onSelect,
}: {
  mode: SessionMode;
  sessions: Session[];
  activeId: string;
  onSelect: (id: string) => void;
}) {
  const Icon = mode === "chat" ? ChatCircle : FolderOpen;

  return (
    <SidebarGroup>
      <SidebarGroupLabel>{mode === "chat" ? "Chat history" : "Work history"}</SidebarGroupLabel>
      <SidebarMenu>
        {sessions.map((session) => (
          <SidebarMenuItem key={session.id}>
            <SidebarMenuButton
              isActive={session.id === activeId}
              onClick={() => onSelect(session.id)}
              tooltip={session.title}
            >
              <Icon weight={mode === "work" ? "fill" : "regular"} />
              <span>{session.title}</span>
            </SidebarMenuButton>
            {mode === "work" ? (
              <SidebarMenuBadge>
                <StatusBadge status={session.status} compact={session.status === "idle"} />
              </SidebarMenuBadge>
            ) : (
              <SidebarMenuBadge>{session.updatedAt}</SidebarMenuBadge>
            )}
          </SidebarMenuItem>
        ))}
      </SidebarMenu>
    </SidebarGroup>
  );
}
