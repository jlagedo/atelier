import { FileCsv, FileXls, FileCode, FileText, Folder, X, type Icon } from "@phosphor-icons/react";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { workspaceFiles, workspaceRoot, type FileKind } from "@/lib/mock-data";

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
      return FileCode;
    default:
      return FileText;
  }
}

export function WorkspacePanel({ onClose }: { onClose: () => void }) {
  return (
    <aside className="bg-sidebar border-sidebar-border hidden w-72 shrink-0 flex-col border-l md:flex">
      <div className="border-sidebar-border flex h-14 shrink-0 items-center gap-2 border-b px-3">
        <Folder weight="fill" className="text-primary size-4" />
        <span className="text-sidebar-foreground text-sm font-medium">Workspace</span>
        <Button
          variant="ghost"
          size="icon"
          className="text-muted-foreground ml-auto size-7"
          onClick={onClose}
          aria-label="Close workspace panel"
        >
          <X />
        </Button>
      </div>

      <div className="border-sidebar-border/60 border-b px-3 py-2">
        <p className="text-muted-foreground truncate font-mono text-[11px]">{workspaceRoot}</p>
      </div>

      <ScrollArea className="flex-1">
        <div className="space-y-0.5 p-2">
          {workspaceFiles.map((file) => {
            const FileIcon = iconFor(file.kind);
            return (
              <button
                key={file.name}
                type="button"
                className="hover:bg-sidebar-accent group flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left transition-colors"
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
                {file.status === "new" && (
                  <span className="bg-primary/15 text-primary rounded px-1.5 py-0.5 text-[10px] font-medium">
                    new
                  </span>
                )}
                {file.status === "modified" && (
                  <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-[10px] font-medium">
                    edited
                  </span>
                )}
                {!file.status && (
                  <span className="text-muted-foreground/70 shrink-0 text-[11px]">
                    {file.modified}
                  </span>
                )}
              </button>
            );
          })}
        </div>
      </ScrollArea>

      <div className="border-sidebar-border/60 text-muted-foreground border-t px-3 py-2 text-[11px]">
        Shared host ↔ VM over 9p · writes gated
      </div>
    </aside>
  );
}
