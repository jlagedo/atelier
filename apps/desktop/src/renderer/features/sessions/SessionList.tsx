import { useState, type ReactNode } from "react";
import { ChatCircle, FolderOpen, Stop, Trash } from "@phosphor-icons/react";

import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { cn } from "@/lib/utils";
import type { Session, SessionMode, SessionStatus } from "@/lib/mock-data";
import { StatusBadge } from "./StatusBadge";

// Statuses with a running in-guest loop — only these can be killed (force-stopped).
const LIVE = new Set<SessionStatus>(["running", "active", "starting", "resuming"]);

function RowAction({
  label,
  icon,
  tone = "default",
  onClick,
}: {
  label: string;
  icon: ReactNode;
  tone?: "default" | "destructive";
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className={cn(
        "ring-sidebar-ring text-sidebar-foreground/65 flex size-6 items-center justify-center rounded-md outline-hidden transition-colors focus-visible:ring-2 [&>svg]:size-3.5",
        tone === "destructive"
          ? "hover:bg-destructive/15 hover:text-destructive"
          : "hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
      )}
    >
      {icon}
    </button>
  );
}

// The right-edge cluster for a WORK row: status badge by default, swapped for
// Kill/Delete actions on hover/focus (and held open while the delete dialog is up).
function WorkRowActions({
  session,
  onKill,
  onDelete,
}: {
  session: Session;
  onKill: (id: string) => void;
  onDelete: (id: string) => void;
}) {
  const [confirmOpen, setConfirmOpen] = useState(false);
  const killable = LIVE.has(session.status);

  return (
    <>
      <div
        aria-hidden={confirmOpen}
        className={cn(
          "pointer-events-none absolute top-1/2 right-1.5 -translate-y-1/2 transition-opacity duration-150",
          "group-hover/menu-item:opacity-0 group-focus-within/menu-item:opacity-0",
          confirmOpen && "opacity-0",
        )}
      >
        <StatusBadge status={session.status} compact={session.status === "idle"} />
      </div>

      <div
        className={cn(
          "absolute top-1/2 right-1 flex -translate-y-1/2 items-center gap-0.5 opacity-0 transition-opacity duration-150",
          "group-hover/menu-item:opacity-100 group-focus-within/menu-item:opacity-100",
          confirmOpen && "opacity-100",
        )}
      >
        {killable && (
          <RowAction
            label="Stop session"
            icon={<Stop weight="fill" />}
            onClick={() => onKill(session.id)}
          />
        )}
        <RowAction
          label="Delete session"
          icon={<Trash />}
          tone="destructive"
          onClick={() => setConfirmOpen(true)}
        />
      </div>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        icon={<Trash />}
        title="Delete this session?"
        description={
          <>
            This permanently removes <span className="text-foreground font-medium">{session.title}</span> and
            its history. The folder on disk is untouched — only the session is deleted.
          </>
        }
        confirmLabel="Delete session"
        onConfirm={() => onDelete(session.id)}
      />
    </>
  );
}

export function SessionList({
  mode,
  sessions,
  activeId,
  onSelect,
  onKill,
  onDelete,
}: {
  mode: SessionMode;
  sessions: Session[];
  activeId: string;
  onSelect: (id: string) => void;
  onKill?: (id: string) => void;
  onDelete?: (id: string) => void;
}) {
  const Icon = mode === "chat" ? ChatCircle : FolderOpen;
  const work = mode === "work" && onKill && onDelete;

  return (
    <SidebarGroup>
      <SidebarGroupLabel>{mode === "chat" ? "Chat history" : "Work history"}</SidebarGroupLabel>
      <SidebarMenu>
        {sessions.map((session) => (
          <SidebarMenuItem key={session.id}>
            <SidebarMenuButton
              isActive={session.id === activeId}
              onClick={() => onSelect(session.id)}
              tooltip={session.title}
              className={work ? "pr-14" : undefined}
            >
              <Icon weight={mode === "work" ? "fill" : "regular"} />
              <span>{session.title}</span>
            </SidebarMenuButton>
            {work ? (
              <WorkRowActions session={session} onKill={onKill} onDelete={onDelete} />
            ) : (
              <SidebarMenuBadge>{session.updatedAt}</SidebarMenuBadge>
            )}
          </SidebarMenuItem>
        ))}
      </SidebarMenu>
    </SidebarGroup>
  );
}
