import { useState } from "react";
import { FolderPlus, Plugs } from "@phosphor-icons/react";

import { ThemeProvider } from "./components/theme-provider";
import { AppSidebar } from "./components/app-sidebar";
import { Button } from "./components/ui/button";
import { SidebarInset, SidebarProvider } from "./components/ui/sidebar";
import { ChatView } from "./features/chat/ChatView";
import { WorkspacePanel } from "./features/workspace/WorkspacePanel";
import { useWorkSessions } from "./hooks/use-work-sessions";
import { defaultSessionIds, sessionsForMode, type Session, type SessionMode } from "./lib/mock-data";

// Shown in WORK mode when there's no active session: either the host service is
// down, or no folder has been opened yet.
function WorkPlaceholder({ hostUp, onNewWork }: { hostUp: boolean | null; onNewWork: () => void }) {
  const down = hostUp === false;
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
      <div className="bg-muted text-muted-foreground flex size-12 items-center justify-center rounded-xl">
        {down ? <Plugs className="size-6" /> : <FolderPlus className="size-6" />}
      </div>
      {down ? (
        <>
          <p className="text-foreground text-sm font-medium">Atelier host service isn&apos;t running</p>
          <p className="text-muted-foreground max-w-sm text-xs">
            Start the elevated host (the broker) to open a work folder. WORK mode drives the in-guest agent
            through it.
          </p>
        </>
      ) : (
        <>
          <p className="text-foreground text-sm font-medium">No work session</p>
          <p className="text-muted-foreground max-w-sm text-xs">
            Open a folder to start a contained agent session. Files you create land in that folder.
          </p>
          <Button onClick={onNewWork}>
            <FolderPlus weight="bold" /> New work
          </Button>
        </>
      )}
    </div>
  );
}

export function App() {
  const [activeMode, setActiveMode] = useState<SessionMode>("chat");
  const [activeChatId, setActiveChatId] = useState<string>(defaultSessionIds.chat);
  const [workspaceOpen, setWorkspaceOpen] = useState(true);
  const work = useWorkSessions();

  const chatSessions = sessionsForMode("chat");
  const activeChat = chatSessions.find((s) => s.id === activeChatId) ?? chatSessions[0];

  const isWork = activeMode === "work";
  const sidebarSessions: Session[] = isWork ? work.list : chatSessions;
  const activeId = isWork ? (work.activeId ?? "") : activeChat.id;
  const activeSession: Session | null = isWork ? work.active : activeChat;

  function onSelect(id: string) {
    if (isWork) work.select(id);
    else setActiveChatId(id);
  }

  function onNewSession() {
    if (isWork) void work.newWork();
    // CHAT mode stays on mock — no new-chat creation in this slice.
  }

  const workComposerDisabled =
    isWork &&
    (work.hostUp === false ||
      activeSession?.status === "starting" ||
      activeSession?.status === "resuming" ||
      activeSession?.status === "hibernating");
  const workComposerHint =
    work.hostUp === false ? "Host service not running — start the Atelier host to work." : undefined;

  return (
    <ThemeProvider>
      <SidebarProvider className="h-svh">
        <AppSidebar
          activeMode={activeMode}
          activeId={activeId}
          sessions={sidebarSessions}
          onModeChange={setActiveMode}
          onSelect={onSelect}
          onNewSession={onNewSession}
        />
        <SidebarInset className="min-w-0">
          {activeSession ? (
            <ChatView
              session={activeSession}
              workspaceOpen={workspaceOpen}
              onToggleWorkspace={() => setWorkspaceOpen((o) => !o)}
              onSubmit={isWork ? work.send : undefined}
              composerDisabled={workComposerDisabled}
              composerHint={isWork ? workComposerHint : undefined}
            />
          ) : (
            <WorkPlaceholder hostUp={work.hostUp} onNewWork={() => void work.newWork()} />
          )}
        </SidebarInset>
        {workspaceOpen && activeSession && (
          <WorkspacePanel session={activeSession} onClose={() => setWorkspaceOpen(false)} />
        )}
      </SidebarProvider>
    </ThemeProvider>
  );
}
