import { useEffect, useRef } from "react";

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
  const messagesRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (isEmpty) return;

    const scrollToLatest = () => {
      const viewport = messagesRef.current;
      if (!viewport) return;
      viewport.scrollTop = viewport.scrollHeight;
    };

    const frame = globalThis.requestAnimationFrame?.(scrollToLatest);
    if (frame === undefined) {
      const timeout = globalThis.setTimeout(scrollToLatest, 0);
      return () => globalThis.clearTimeout(timeout);
    }
    return () => globalThis.cancelAnimationFrame?.(frame);
  }, [isEmpty, session.id, session.items]);

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
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
        <div ref={messagesRef} className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
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
