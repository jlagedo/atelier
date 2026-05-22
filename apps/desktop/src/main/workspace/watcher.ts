// One watcher per WORK session (design.md §11): watch the session's host folder,
// debounce bursts, and push a refreshed tree + the changed-files diff (against the
// open-time baseline). Best-effort: fs.watch semantics vary by OS, so we re-scan
// on any event rather than trusting per-event paths.

import fs from "node:fs";
import { diffChanged, readTree, snapshot, type ChangedFile, type Snapshot, type WorkspaceFile } from "./reader";

export interface WorkspaceUpdate {
  files: WorkspaceFile[];
  changedFiles: ChangedFile[];
}

export class WorkspaceWatcher {
  private watcher?: fs.FSWatcher;
  private baseline?: Snapshot;
  private timer?: NodeJS.Timeout;
  private disposed = false;

  constructor(
    private readonly folder: string,
    private readonly onUpdate: (u: WorkspaceUpdate) => void,
    private readonly debounceMs = 400,
  ) {}

  /** Snapshot the baseline, push the initial tree, then watch for changes. */
  async start(): Promise<void> {
    this.baseline = await snapshot(this.folder);
    await this.emit();
    try {
      this.watcher = fs.watch(this.folder, { recursive: true }, () => this.schedule());
      this.watcher.on("error", () => {
        /* ignore — a transient watch error shouldn't crash main */
      });
    } catch {
      // recursive watch unsupported here; the initial tree still shows, and an
      // explicit refresh() can be wired later.
    }
  }

  /** Force a re-scan + push (e.g. after a tool run completes). */
  async refresh(): Promise<void> {
    await this.emit();
  }

  dispose(): void {
    this.disposed = true;
    if (this.timer) clearTimeout(this.timer);
    this.watcher?.close();
    this.watcher = undefined;
  }

  private schedule(): void {
    if (this.disposed) return;
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.emit(), this.debounceMs);
  }

  private async emit(): Promise<void> {
    if (this.disposed) return;
    const base = this.baseline ?? { mtimes: new Map<string, number>() };
    const [files, changedFiles] = await Promise.all([readTree(this.folder), diffChanged(this.folder, base)]);
    if (!this.disposed) this.onUpdate({ files, changedFiles });
  }
}
