import { useEffect, useState } from "react";
import { ChatCircle, NotePencil } from "@phosphor-icons/react";

import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { conversations } from "@/lib/mock-data";

export function AppSidebar({
  activeId,
  onSelect,
}: {
  activeId: string;
  onSelect: (id: string) => void;
}) {
  const [version, setVersion] = useState("…");

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
              <div className="bg-primary text-primary-foreground flex aspect-square size-8 items-center justify-center rounded-md">
                <span className="font-serif text-lg leading-none font-semibold">A</span>
              </div>
              <div className="flex flex-col gap-0.5 leading-none">
                <span className="font-serif text-base font-semibold tracking-tight">Atelier</span>
                <span className="text-muted-foreground text-[11px]">Eliza workspace</span>
              </div>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="New chat"
                className="text-primary hover:text-primary font-medium"
              >
                <NotePencil weight="bold" />
                <span>New chat</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>Conversations</SidebarGroupLabel>
          <SidebarMenu>
            {conversations.map((conversation) => (
              <SidebarMenuItem key={conversation.id}>
                <SidebarMenuButton
                  isActive={conversation.id === activeId}
                  onClick={() => onSelect(conversation.id)}
                  tooltip={conversation.title}
                >
                  <ChatCircle />
                  <span>{conversation.title}</span>
                </SidebarMenuButton>
                <SidebarMenuBadge>{conversation.updatedAt}</SidebarMenuBadge>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
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
