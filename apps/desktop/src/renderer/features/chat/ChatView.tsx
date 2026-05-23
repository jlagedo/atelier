import { ChatHeader } from "./ChatHeader";
import { MessageBubble } from "./MessageBubble";
import { ToolCallCard } from "./ToolCallCard";
import { PolicyDecisionCard } from "./PolicyDecisionCard";
import { Composer } from "./Composer";
import { EmptyState } from "./EmptyState";
import type { Session } from "@/lib/mock-data";

export function ChatView({
  session,
  workspaceOpen,
  onToggleWorkspace,
  onSubmit,
  composerDisabled,
  composerHint,
}: {
  session: Session;
  workspaceOpen: boolean;
  onToggleWorkspace: () => void;
  onSubmit?: (text: string) => void;
  composerDisabled?: boolean;
  composerHint?: string;
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
          <div className="mx-auto max-w-reading space-y-stack px-gutter py-12">
            {session.items.map((item) => (
              <div
                key={item.id}
                className="animate-in fade-in slide-in-from-bottom-2 fill-mode-backwards duration-500"
              >
                {item.kind === "message" && <MessageBubble role={item.role} content={item.content} />}
                {item.kind === "tool" && <ToolCallCard tool={item.tool} />}
                {item.kind === "policy" && <PolicyDecisionCard policy={item.policy} />}
              </div>
            ))}
          </div>
        </div>
      )}

      <Composer mode={session.mode} onSubmit={onSubmit} disabled={composerDisabled} hint={composerHint} />
    </div>
  );
}
