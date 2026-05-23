import { useState } from "react";
import { PaperPlaneRight, Paperclip } from "@phosphor-icons/react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { SessionMode } from "@/lib/mock-data";

export function Composer({
  mode,
  onSubmit,
  disabled = false,
  hint,
}: {
  mode: SessionMode;
  onSubmit?: (text: string) => void;
  disabled?: boolean;
  hint?: string;
}) {
  const [value, setValue] = useState("");
  const isWork = mode === "work";
  const live = typeof onSubmit === "function";

  function submit() {
    const text = value.trim();
    if (text.length === 0 || disabled) return;
    if (live) onSubmit?.(text);
    // Chat mode (mock) just clears; WORK mode sends to the in-guest loop.
    setValue("");
  }

  const defaultHint = isWork
    ? "Connected to the in-guest agent · file + shell tools allowed, network denied by policy."
    : "Mock interface — no agent connected. Chat sessions do not map a work folder.";

  return (
    <div className="border-border bg-background border-t px-6 py-5">
      <div className="border-input bg-card focus-within:border-ring/60 focus-within:ring-ring/20 mx-auto flex max-w-reading items-end gap-2 rounded-2xl border px-4 py-2.5 transition-[border-color,box-shadow] focus-within:ring-[3px]">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="text-muted-foreground size-8 shrink-0"
          aria-label="Attach file"
        >
          <Paperclip />
        </Button>
        <Textarea
          rows={1}
          value={value}
          disabled={disabled}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              submit();
            }
          }}
          placeholder={isWork ? "Ask Atelier to work in this folder..." : "Message Atelier..."}
          className="max-h-40 min-h-0 resize-none border-0 bg-transparent px-1 py-1.5 shadow-none focus-visible:ring-0"
        />
        <Button
          type="button"
          size="icon"
          className="enabled:shadow-brass size-8 shrink-0 rounded-xl transition-all enabled:hover:brightness-105"
          aria-label="Send message"
          disabled={value.trim().length === 0 || disabled}
          onClick={submit}
        >
          <PaperPlaneRight weight="fill" />
        </Button>
      </div>
      <p className="text-muted-foreground/70 mt-3 text-center text-[11px]">{hint ?? defaultHint}</p>
    </div>
  );
}
