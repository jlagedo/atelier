import * as React from "react";
import * as Dialog from "@radix-ui/react-dialog";

import { cn } from "@/lib/utils";
import { Button } from "./button";

// A small, focused confirm modal for irreversible actions (built on Radix Dialog —
// the repo has no AlertDialog primitive). Themed styling: lamp shadow, Fraunces
// display title, a tinted glyph that takes the action's tone.
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  onConfirm,
  icon,
  destructive = true,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: React.ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  onConfirm: () => void;
  icon?: React.ReactNode;
  destructive?: boolean;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay
          className={cn(
            "fixed inset-0 z-50 bg-foreground/25 backdrop-blur-[2px]",
            "data-[state=open]:animate-in data-[state=closed]:animate-out",
            "data-[state=open]:fade-in-0 data-[state=closed]:fade-out-0",
          )}
        />
        <Dialog.Content
          onClick={(e) => e.stopPropagation()}
          className={cn(
            "fixed top-1/2 left-1/2 z-50 w-[calc(100%-2rem)] max-w-sm -translate-x-1/2 -translate-y-1/2",
            "border-border bg-background shadow-lamp rounded-2xl border p-6",
            "data-[state=open]:animate-in data-[state=closed]:animate-out",
            "data-[state=open]:fade-in-0 data-[state=closed]:fade-out-0",
            "data-[state=open]:zoom-in-95 data-[state=closed]:zoom-out-95 duration-150",
          )}
        >
          <div className="flex flex-col items-start gap-4">
            {icon && (
              <div
                className={cn(
                  "flex size-11 items-center justify-center rounded-xl [&>svg]:size-5",
                  destructive ? "bg-destructive/12 text-destructive" : "bg-primary/12 text-primary",
                )}
              >
                {icon}
              </div>
            )}
            <div className="flex flex-col gap-1.5">
              <Dialog.Title className="font-display text-foreground text-lg font-semibold tracking-tight">
                {title}
              </Dialog.Title>
              <Dialog.Description className="text-muted-foreground text-sm leading-relaxed">
                {description}
              </Dialog.Description>
            </div>
          </div>
          <div className="mt-6 flex justify-end gap-2">
            <Dialog.Close asChild>
              <Button variant="outline" size="sm">
                {cancelLabel}
              </Button>
            </Dialog.Close>
            <Button
              variant={destructive ? "destructive" : "default"}
              size="sm"
              onClick={() => {
                onConfirm();
                onOpenChange(false);
              }}
            >
              {confirmLabel}
            </Button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
