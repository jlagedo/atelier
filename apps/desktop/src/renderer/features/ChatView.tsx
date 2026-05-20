import { clsx } from "clsx";
import { PaperPlaneRight, ShieldCheck, Wrench } from "@phosphor-icons/react";

type Message = {
  id: string;
  role: "user" | "assistant";
  text: string;
};

const MOCK_MESSAGES: Message[] = [
  {
    id: "1",
    role: "user",
    text: "Summarise orders.csv in my workspace and flag any duplicate order IDs.",
  },
  {
    id: "2",
    role: "assistant",
    text: "I'll read the file, scan for duplicate order IDs, and write a short summary back to your workspace.",
  },
];

function MessageBubble({ message }: { message: Message }) {
  const isUser = message.role === "user";
  return (
    <div className={clsx("flex", isUser ? "justify-end" : "justify-start")}>
      <div
        className={clsx(
          "max-w-[80%] rounded-2xl px-4 py-2 text-sm leading-relaxed",
          isUser ? "bg-blue-600 text-white" : "bg-neutral-800 text-neutral-100",
        )}
      >
        {message.text}
      </div>
    </div>
  );
}

function ToolCallCard() {
  return (
    <div className="rounded-xl border border-neutral-800 bg-neutral-900/60">
      <div className="flex items-center gap-2 px-3 py-2 text-xs text-neutral-400">
        <Wrench size={14} weight="bold" />
        <span className="font-medium text-neutral-300">ran python</span>
        <span className="text-neutral-600">·</span>
        <span>scan_duplicates.py</span>
      </div>
      <pre className="overflow-x-auto px-3 pb-3 text-xs text-neutral-500">
        {`> found 3 duplicate order IDs\n> wrote summary.md (412 bytes)`}
      </pre>
    </div>
  );
}

function ApprovalPrompt() {
  return (
    <div className="flex items-center justify-between gap-3 rounded-xl border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm">
      <div className="flex items-center gap-2 text-amber-200">
        <ShieldCheck size={16} weight="bold" />
        <span>
          Write <code className="rounded bg-black/30 px-1">summary.md</code> to your workspace?
        </span>
      </div>
      <div className="flex shrink-0 gap-2">
        <button
          type="button"
          className="rounded-lg bg-amber-500 px-3 py-1 text-xs font-medium text-black"
        >
          Approve
        </button>
        <button
          type="button"
          className="rounded-lg border border-neutral-700 px-3 py-1 text-xs text-neutral-300"
        >
          Deny
        </button>
      </div>
    </div>
  );
}

export function ChatView() {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex-1 space-y-4 overflow-y-auto px-4 py-6">
        {MOCK_MESSAGES.map((message) => (
          <MessageBubble key={message.id} message={message} />
        ))}
        <ToolCallCard />
        <ApprovalPrompt />
      </div>

      <form
        className="border-t border-neutral-800 p-3"
        onSubmit={(event) => event.preventDefault()}
      >
        <div className="flex items-end gap-2 rounded-2xl border border-neutral-800 bg-neutral-900 px-3 py-2">
          <textarea
            rows={1}
            placeholder="Message Atelier…"
            className="flex-1 resize-none bg-transparent text-sm text-neutral-100 placeholder:text-neutral-600 focus:outline-none"
          />
          <button
            type="submit"
            aria-label="Send message"
            className="rounded-xl bg-blue-600 p-2 text-white"
          >
            <PaperPlaneRight size={16} weight="fill" />
          </button>
        </div>
        <p className="mt-2 text-center text-[11px] text-neutral-600">
          Mock layout — feature 0 scaffold. No agent connected.
        </p>
      </form>
    </div>
  );
}
