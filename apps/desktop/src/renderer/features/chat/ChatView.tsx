import { ChatHeader } from "./ChatHeader";
import { MessageBubble } from "./MessageBubble";
import { ToolCallCard } from "./ToolCallCard";
import { ApprovalPrompt } from "./ApprovalPrompt";
import { Composer } from "./Composer";
import { EmptyState } from "./EmptyState";
import type { Session } from "@/lib/mock-data";

export function ChatView({
  session,
  workspaceOpen,
  onToggleWorkspace,
}: {
  session: Session;
  workspaceOpen: boolean;
  onToggleWorkspace: () => void;
}) {
  const isEmpty = session.items.length === 0;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ChatHeader
        session={session}
        workspaceOpen={workspaceOpen}
        onToggleWorkspace={onToggleWorkspace}
      />

      {isEmpty ? (
        <div className="min-h-0 flex-1">
          <EmptyState />
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto max-w-3xl space-y-6 px-4 py-8">
            {session.items.map((item) => {
              switch (item.kind) {
                case "message":
                  return <MessageBubble key={item.id} role={item.role} content={item.content} />;
                case "tool":
                  return <ToolCallCard key={item.id} tool={item.tool} />;
                case "approval":
                  return <ApprovalPrompt key={item.id} approval={item.approval} />;
              }
            })}
          </div>
        </div>
      )}

      <Composer mode={session.mode} />
    </div>
  );
}
