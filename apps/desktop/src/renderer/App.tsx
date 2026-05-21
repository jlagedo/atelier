import { useState } from "react";

import { ThemeProvider } from "./components/theme-provider";
import { AppSidebar } from "./components/app-sidebar";
import { SidebarInset, SidebarProvider } from "./components/ui/sidebar";
import { ChatView } from "./features/chat/ChatView";
import { WorkspacePanel } from "./features/workspace/WorkspacePanel";
import {
  defaultSessionIds,
  sessionsForMode,
  type SessionMode,
} from "./lib/mock-data";

export function App() {
  const [activeMode, setActiveMode] = useState<SessionMode>("chat");
  const [activeSessionByMode, setActiveSessionByMode] =
    useState<Record<SessionMode, string>>(defaultSessionIds);
  const [workspaceOpen, setWorkspaceOpen] = useState(true);

  const visibleSessions = sessionsForMode(activeMode);
  const activeId = activeSessionByMode[activeMode];
  const activeSession =
    visibleSessions.find((session) => session.id === activeId) ?? visibleSessions[0];

  function selectMode(mode: SessionMode) {
    setActiveMode(mode);
  }

  function selectSession(id: string) {
    setActiveSessionByMode((current) => ({ ...current, [activeMode]: id }));
  }

  return (
    <ThemeProvider>
      <SidebarProvider className="h-svh">
        <AppSidebar
          activeMode={activeMode}
          activeId={activeSession.id}
          sessions={visibleSessions}
          onModeChange={selectMode}
          onSelect={selectSession}
        />
        <SidebarInset className="min-w-0">
          <ChatView
            session={activeSession}
            workspaceOpen={workspaceOpen}
            onToggleWorkspace={() => setWorkspaceOpen((o) => !o)}
          />
        </SidebarInset>
        {workspaceOpen && (
          <WorkspacePanel session={activeSession} onClose={() => setWorkspaceOpen(false)} />
        )}
      </SidebarProvider>
    </ThemeProvider>
  );
}
