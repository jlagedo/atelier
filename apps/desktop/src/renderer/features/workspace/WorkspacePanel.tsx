import {
  CheckCircle,
  CircleNotch,
  FileCode,
  FileCsv,
  FileText,
  FileXls,
  Folder,
  LockKey,
  X,
  type Icon,
} from "@phosphor-icons/react";

import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Section } from "@/components/ui/section";
import type { BackgroundTask, FileKind, Session, WorkspaceFile, WorkSession } from "@/lib/mock-data";

function iconFor(kind: FileKind): Icon {
  switch (kind) {
    case "folder":
      return Folder;
    case "csv":
      return FileCsv;
    case "xlsx":
      return FileXls;
    case "py":
    case "json":
    case "ts":
      return FileCode;
    default:
      return FileText;
  }
}

function FileList({ files }: { files: WorkspaceFile[] }) {
  return (
    <div className="space-y-0.5">
      {files.map((file) => {
        const FileIcon = iconFor(file.kind);
        return (
          <button
            key={file.name}
            type="button"
            className="hover:bg-sidebar-accent group flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors"
          >
            <FileIcon
              weight={file.kind === "folder" ? "fill" : "regular"}
              className={cn(
                "size-4 shrink-0",
                file.kind === "folder" ? "text-primary/80" : "text-muted-foreground",
              )}
            />
            <span className="text-sidebar-foreground min-w-0 flex-1 truncate text-sm">
              {file.name}
            </span>
            {file.status === "new" && <Badge tone="accent">new</Badge>}
            {file.status === "modified" && <Badge tone="muted">edited</Badge>}
            {!file.status && (
              <span className="text-muted-foreground/70 shrink-0 text-[11px]">{file.modified}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

function TaskIcon({ status }: { status: BackgroundTask["status"] }) {
  if (status === "running") return <CircleNotch className="text-signal size-3.5 animate-spin" />;
  return <CheckCircle weight="fill" className="text-positive size-3.5" />;
}

function AccessRow({ label, value, icon }: { label: string; value: "allowed" | "denied"; icon?: boolean }) {
  const denied = value === "denied";
  return (
    <div className="bg-sidebar-accent/50 flex items-center justify-between rounded-md px-2.5 py-2 text-xs">
      <span className="text-muted-foreground flex items-center gap-1.5">
        {icon && <LockKey className="size-3.5" />} {label}
      </span>
      <span
        className={cn(
          "flex items-center gap-1.5 font-medium",
          denied ? "text-destructive" : "text-positive",
        )}
      >
        <span className={cn("size-1.5 rounded-full", denied ? "bg-destructive" : "bg-positive")} />
        {value}
      </span>
    </div>
  );
}

function PanelHeader({ session, onClose }: { session: Session; onClose: () => void }) {
  const isWork = session.mode === "work";

  return (
    <div className="border-sidebar-border flex h-header shrink-0 items-center gap-2 border-b px-4">
      <Folder weight={isWork ? "fill" : "regular"} className="text-primary size-4" />
      <span className="text-sidebar-foreground text-sm font-medium">
        {isWork ? "Work context" : "Chat context"}
      </span>
      <Button
        variant="ghost"
        size="icon"
        className="text-muted-foreground ml-auto size-7"
        onClick={onClose}
        aria-label={isWork ? "Close work context panel" : "Close chat context panel"}
      >
        <X />
      </Button>
    </div>
  );
}

function ChatContextPanel({ session }: { session: Extract<Session, { mode: "chat" }> }) {
  return (
    <ScrollArea className="flex-1">
      <div className="space-y-section p-4">
        <Section title="Session">
          <div className="border-sidebar-border bg-background/40 rounded-lg border p-3">
            <p className="text-sidebar-foreground text-sm">{session.preview}</p>
            <p className="text-muted-foreground mt-1 text-[11px]">Chat only · no work folder</p>
          </div>
        </Section>

        <Section title="Notes">
          <div className="space-y-1.5">
            {(session.notes.length > 0 ? session.notes : ["No pinned notes yet"]).map((note) => (
              <div
                key={note}
                className="bg-sidebar-accent/50 text-muted-foreground rounded-md px-2.5 py-2 text-xs"
              >
                {note}
              </div>
            ))}
          </div>
        </Section>

        <Section title="Artifacts">
          <div className="space-y-1.5">
            {session.artifacts.map((artifact) => (
              <div key={artifact} className="flex items-center gap-2 rounded-md px-2.5 py-2">
                <FileText className="text-muted-foreground size-4" />
                <span className="text-sidebar-foreground text-sm">{artifact}</span>
              </div>
            ))}
          </div>
        </Section>
      </div>
    </ScrollArea>
  );
}

function WorkContextPanel({ session }: { session: WorkSession }) {
  return (
    <>
      <div className="border-sidebar-border/60 border-b px-3 py-2">
        <p className="text-muted-foreground truncate font-mono text-[11px]">{session.folderPath}</p>
      </div>

      <ScrollArea className="flex-1">
        <div className="space-y-section p-4">
          <Section title="Access" hint="fixed by policy">
            <div className="grid gap-1.5">
              <AccessRow label="Files" value={session.access.files} icon />
              <AccessRow label="Shell" value={session.access.shell} />
              <AccessRow label="Network" value={session.access.network} />
            </div>
          </Section>

          <Section title="Running tasks">
            <div className="space-y-1.5">
              {session.backgroundTasks.map((task) => (
                <div key={task.id} className="flex items-center gap-2 rounded-md px-2.5 py-2">
                  <TaskIcon status={task.status} />
                  <span className="text-sidebar-foreground min-w-0 flex-1 truncate text-sm">
                    {task.label}
                  </span>
                  <span className="text-muted-foreground text-[10px]">{task.status}</span>
                </div>
              ))}
            </div>
          </Section>

          <Section title="Changed files">
            <div className="space-y-1.5">
              {session.changedFiles.map((file) => (
                <div key={file.path} className="rounded-md px-2.5 py-2">
                  <p className="text-sidebar-foreground truncate font-mono text-xs">{file.path}</p>
                  <p className="text-muted-foreground text-[10px]">{file.status}</p>
                </div>
              ))}
            </div>
          </Section>

          <Section title={session.folderName}>
            <FileList files={session.files} />
          </Section>
        </div>
      </ScrollArea>

      <div className="border-sidebar-border/60 text-muted-foreground border-t px-3 py-2 text-[11px]">
        Shared host to VM over 9p · writes gated
      </div>
    </>
  );
}

export function WorkspacePanel({ session, onClose }: { session: Session; onClose: () => void }) {
  return (
    <aside className="bg-sidebar border-sidebar-border hidden w-panel shrink-0 flex-col border-l md:flex">
      <PanelHeader session={session} onClose={onClose} />
      {session.mode === "work" ? (
        <WorkContextPanel session={session} />
      ) : (
        <ChatContextPanel session={session} />
      )}
    </aside>
  );
}
