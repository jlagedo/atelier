// The "Work context" panel's data source (design.md §11): the session's HOST
// folder IS its 9p share, so we read the tree directly from the host fs — no guest
// round-trip. One level deep (folders + files), matching the mock's shape.

import { promises as fs } from "node:fs";
import path from "node:path";

export type FileKind = "csv" | "xlsx" | "md" | "py" | "txt" | "json" | "folder" | "ts" | "other";

export interface WorkspaceFile {
  name: string;
  kind: FileKind;
  size?: string;
  modified?: string;
  status?: "new" | "modified";
}

export interface ChangedFile {
  path: string;
  status: "created" | "modified";
}

const EXT_KIND: Record<string, FileKind> = {
  ".csv": "csv",
  ".xlsx": "xlsx",
  ".md": "md",
  ".py": "py",
  ".txt": "txt",
  ".json": "json",
  ".ts": "ts",
  ".tsx": "ts",
};

function kindOf(name: string): FileKind {
  return EXT_KIND[path.extname(name).toLowerCase()] ?? "other";
}

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

function relativeTime(mtimeMs: number, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - mtimeMs) / 1000));
  if (s < 5) return "just now";
  if (s < 60) return `${s}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}

// Inference here pins Dirent<string> (vs the @types/node Buffer overload).
async function safeReaddir(dir: string) {
  try {
    return await fs.readdir(dir, { withFileTypes: true });
  } catch {
    return [];
  }
}

/** Read the top level of a folder into the panel's WorkspaceFile[] (folders first). */
export async function readTree(folder: string, now = Date.now()): Promise<WorkspaceFile[]> {
  const entries = await safeReaddir(folder);
  const out: WorkspaceFile[] = [];
  for (const e of entries) {
    if (e.name.startsWith(".")) continue; // hide dotfiles in the panel
    const full = path.join(folder, e.name);
    if (e.isDirectory()) {
      let modified: string | undefined;
      try {
        modified = relativeTime((await fs.stat(full)).mtimeMs, now);
      } catch {
        /* ignore */
      }
      out.push({ name: e.name, kind: "folder", modified });
      continue;
    }
    try {
      const st = await fs.stat(full);
      out.push({ name: e.name, kind: kindOf(e.name), size: humanSize(st.size), modified: relativeTime(st.mtimeMs, now) });
    } catch {
      out.push({ name: e.name, kind: kindOf(e.name) });
    }
  }
  out.sort((a, b) => {
    if (a.kind === "folder" && b.kind !== "folder") return -1;
    if (a.kind !== "folder" && b.kind === "folder") return 1;
    return a.name.localeCompare(b.name);
  });
  return out;
}

export interface Snapshot {
  mtimes: Map<string, number>;
}

/** Snapshot file mtimes recursively (for the changed-files diff), skipping dotdirs. */
export async function snapshot(folder: string): Promise<Snapshot> {
  const mtimes = new Map<string, number>();
  async function walk(dir: string): Promise<void> {
    const entries = await safeReaddir(dir);
    for (const e of entries) {
      if (e.name.startsWith(".")) continue;
      const full = path.join(dir, e.name);
      if (e.isDirectory()) {
        await walk(full);
      } else {
        try {
          mtimes.set(full, (await fs.stat(full)).mtimeMs);
        } catch {
          /* ignore */
        }
      }
    }
  }
  await walk(folder);
  return { mtimes };
}

/** Diff the current state against a baseline snapshot into ChangedFile[] (created/modified). */
export async function diffChanged(folder: string, base: Snapshot): Promise<ChangedFile[]> {
  const now = await snapshot(folder);
  const changed: ChangedFile[] = [];
  for (const [full, mtime] of now.mtimes) {
    const prev = base.mtimes.get(full);
    const rel = path.relative(folder, full).split(path.sep).join("/");
    if (prev === undefined) changed.push({ path: rel, status: "created" });
    else if (mtime > prev) changed.push({ path: rel, status: "modified" });
  }
  changed.sort((a, b) => a.path.localeCompare(b.path));
  return changed;
}
