import { useState } from "react";
import { PaperPlaneRight, Paperclip } from "@phosphor-icons/react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

export function Composer() {
  const [value, setValue] = useState("");

  function submit() {
    // Mock — no agent connected. Just clear the field.
    setValue("");
  }

  return (
    <div className="border-border bg-background border-t px-4 py-3">
      <div className="border-input bg-card focus-within:border-ring/60 focus-within:ring-ring/20 mx-auto flex max-w-3xl items-end gap-2 rounded-2xl border px-3 py-2 transition-[border-color,box-shadow] focus-within:ring-[3px]">
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
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              submit();
            }
          }}
          placeholder="Message Atelier…"
          className="max-h-40 min-h-0 resize-none border-0 bg-transparent px-1 py-1.5 shadow-none focus-visible:ring-0"
        />
        <Button
          type="button"
          size="icon"
          className="size-8 shrink-0 rounded-xl"
          aria-label="Send message"
          disabled={value.trim().length === 0}
          onClick={submit}
        >
          <PaperPlaneRight weight="fill" />
        </Button>
      </div>
      <p className="text-muted-foreground/70 mt-2 text-center text-[11px]">
        Mock interface — no agent connected. Files and Python run inside the sandboxed VM.
      </p>
    </div>
  );
}
