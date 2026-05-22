// Durable context store (S6.1): persists each WORK session's resumable context to
// disk so the work-session list rebuilds on app restart and any dormant session is
// resumable. Keyed by appId; holds the SDK session_id (the resume handle) plus the
// transcript (renderable events) so the chat view can be rebuilt without the loop.
// Electron-free (takes a dir) so it's unit-testable; main.ts passes userData.

import { promises as fs } from "node:fs";
import path from "node:path";

export type StoredStatus = "active" | "inactive";

export interface StoredSession {
  appId: string;
  folder: string;
  title: string;
  /** SDK session_id captured from the loop's init event; "" until first turn. */
  sdkSessionId: string;
  /** Renderable events (text/tool_use/tool_result/policy/result) for rebuild. */
  transcript: unknown[];
  status: StoredStatus;
  updatedAt: number;
}

export class SessionStore {
  private readonly file: string;
  private readonly cache = new Map<string, StoredSession>();
  private loaded = false;
  private writing: Promise<void> = Promise.resolve();

  constructor(dir: string) {
    this.file = path.join(dir, "work-sessions.json");
  }

  async load(): Promise<void> {
    if (this.loaded) return;
    try {
      const raw = await fs.readFile(this.file, "utf8");
      const arr = JSON.parse(raw) as StoredSession[];
      for (const s of arr) this.cache.set(s.appId, s);
    } catch {
      // No file yet (first run) — start empty.
    }
    this.loaded = true;
  }

  list(): StoredSession[] {
    return [...this.cache.values()].sort((a, b) => b.updatedAt - a.updatedAt);
  }

  get(appId: string): StoredSession | undefined {
    return this.cache.get(appId);
  }

  async save(s: StoredSession): Promise<void> {
    this.cache.set(s.appId, { ...s, updatedAt: Date.now() });
    await this.flush();
  }

  /** Shallow-merge a partial update onto an existing entry (no-op if absent). */
  async patch(appId: string, partial: Partial<StoredSession>): Promise<void> {
    const cur = this.cache.get(appId);
    if (!cur) return;
    this.cache.set(appId, { ...cur, ...partial, updatedAt: Date.now() });
    await this.flush();
  }

  async remove(appId: string): Promise<void> {
    if (this.cache.delete(appId)) await this.flush();
  }

  // Serialize writes so concurrent saves don't interleave/corrupt the file.
  private async flush(): Promise<void> {
    const snapshot = JSON.stringify([...this.cache.values()], null, 2);
    this.writing = this.writing.then(async () => {
      await fs.mkdir(path.dirname(this.file), { recursive: true });
      await fs.writeFile(this.file, snapshot, "utf8");
    });
    await this.writing;
  }
}
