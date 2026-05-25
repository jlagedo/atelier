import { cn } from "@/lib/utils";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Markdown } from "./Markdown";
import type { Role } from "@/lib/mock-data";

export function MessageBubble({ role, content }: { role: Role; content: string }) {
  if (role === "user") {
    return (
      <div className="flex justify-end">
        <div
          className={cn(
            "bg-secondary text-secondary-foreground max-w-[80%] rounded-2xl rounded-br-sm px-4 py-3",
            "ring-border/50 ring-1",
          )}
        >
          <Markdown className="prose-p:my-0">{content}</Markdown>
        </div>
      </div>
    );
  }

  return (
    <div className="flex gap-msg-gap">
      <Avatar className="bg-signal/12 ring-signal/25 mt-0.5 size-avatar ring-1">
        <AvatarFallback className="bg-transparent">
          <span className="text-signal font-display text-sm font-semibold">A</span>
        </AvatarFallback>
      </Avatar>
      <div className="min-w-0 flex-1 pt-0.5">
        <Markdown>{content}</Markdown>
      </div>
    </div>
  );
}
