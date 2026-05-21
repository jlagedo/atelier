import { useState } from "react";

import { ThemeProvider } from "./components/theme-provider";
import { AppSidebar } from "./components/app-sidebar";
import { SidebarInset, SidebarProvider } from "./components/ui/sidebar";
import { ChatView } from "./features/chat/ChatView";
import { WorkspacePanel } from "./features/workspace/WorkspacePanel";
import { activeConversationId, conversations } from "./lib/mock-data";

export function App() {
  const [activeId, setActiveId] = useState(activeConversationId);
  const [workspaceOpen, setWorkspaceOpen] = useState(true);

  const conversation = conversations.find((c) => c.id === activeId) ?? conversations[0];

  return (
    <ThemeProvider>
      <SidebarProvider className="h-svh">
        <AppSidebar activeId={activeId} onSelect={setActiveId} />
        <SidebarInset className="min-w-0">
          <ChatView
            conversation={conversation}
            workspaceOpen={workspaceOpen}
            onToggleWorkspace={() => setWorkspaceOpen((o) => !o)}
          />
        </SidebarInset>
        {workspaceOpen && <WorkspacePanel onClose={() => setWorkspaceOpen(false)} />}
      </SidebarProvider>
    </ThemeProvider>
  );
}
