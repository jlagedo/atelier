// Session Manager (S6.1): the host-owned state machine for WORK sessions over ONE
// shared VM. It brings up vm0 once, then per session: mounts the folder at
// /sessions/<appId>, launches a PERSISTENT in-guest agent loop (cli-guest --serve),
// feeds turns via execInput, and streams the loop's NDJSON events to the renderer.
// To bound guest memory it caps live loops: an idle timer and a max-active LRU both
// HIBERNATE a session — export its context to the durable store, kill the loop,
// detach the mount. Selecting a dormant session RESUMES it (re-mount + --resume).
//
// Two ids per session: appId (ours — the guest exec session id for stdin routing,
// the 9p tag, and the /sessions/<appId> path) and sdkSessionId (the SDK
// conversation id captured from the loop's init event; the --resume handle).

import crypto from "node:crypto";
import path from "node:path";
import { HostClient, type ExecRun, type OutputStream } from "../host-client";
import type { LoopControl, LoopEvent } from "../host-client/types";
import { WorkspaceWatcher, type WorkspaceUpdate } from "../workspace/watcher";
import { guestdImageFileName, resolveBundleDir, rootfsFileName } from "./bundle";
import { SessionStore } from "./store";

export type SessionLifecycle = "starting" | "active" | "hibernating" | "inactive" | "resuming" | "error";

export interface SessionSummary {
  appId: string;
  title: string;
  folder: string;
  status: SessionLifecycle;
  updatedAt: number;
  transcript: unknown[];
}

export interface ManagerEmitter {
  status(appId: string, status: SessionLifecycle, meta?: Record<string, unknown>): void;
  event(appId: string, ev: LoopEvent): void;
  files(appId: string, update: WorkspaceUpdate): void;
  host(up: boolean): void;
}

export interface ManagerOptions {
  vmId?: string;
  bundleDir?: string;
  bundleBaseDir?: string; // parent of the per-target bundle subdir (injected by handlers.ts)
  platform?: NodeJS.Platform; // override for tests
  arch?: string; // override for tests
  egressAllow?: string[];
  bootTimeoutMs?: number;
  idleMs?: number;
  maxActive?: number;
}

const GUEST_TSX = "/opt/atelier/packages/agent/node_modules/.bin/tsx";
const GUEST_CWD = "/opt/atelier/packages/agent";
const GUEST_AGENT = "src/cli-guest.ts";

// Events worth persisting as the rebuildable chat transcript.
const RENDERABLE = new Set(["text", "tool_use", "tool_result", "policy", "result"]);

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
const b64line = (obj: LoopControl) => Buffer.from(JSON.stringify(obj) + "\n", "utf8").toString("base64");

interface LiveSession {
  appId: string;
  folder: string;
  title: string;
  tag: string;
  guestPath: string;
  status: SessionLifecycle;
  sdkSessionId: string;
  run?: ExecRun;
  watcher?: WorkspaceWatcher;
  stdoutBuf: string;
  transcript: unknown[];
  lastActivity: number;
  idleTimer?: NodeJS.Timeout;
  intentionalStop: boolean;
  exportWaiter?: { resolve: (ev: Extract<LoopEvent, { type: "context" }>) => void; reject: (e: unknown) => void };
}

export class SessionManager {
  private readonly live = new Map<string, LiveSession>();
  private readonly vmId: string;
  private readonly bundleDir: string;
  private readonly rootfsName: string;
  private readonly guestdImageName: string;
  private readonly egressAllow: string[];
  private readonly bootTimeoutMs: number;
  private readonly idleMs: number;
  private readonly maxActive: number;
  private vmReady?: Promise<void>;

  constructor(
    private readonly host: HostClient,
    private readonly store: SessionStore,
    private readonly emit: ManagerEmitter,
    opts: ManagerOptions = {},
  ) {
    this.vmId = opts.vmId ?? "vm0";
    const platform = opts.platform ?? process.platform;
    this.bundleDir = resolveBundleDir({
      optsBundleDir: opts.bundleDir,
      baseDir: opts.bundleBaseDir ?? path.join(process.cwd(), "image", "bundle"),
      platform,
      arch: opts.arch,
    });
    this.rootfsName = rootfsFileName(platform);
    this.guestdImageName = guestdImageFileName(platform);
    this.egressAllow = opts.egressAllow ?? ["api.anthropic.com"];
    this.bootTimeoutMs = opts.bootTimeoutMs ?? (Number(process.env.ATELIER_BOOT_TIMEOUT_MS) || 120_000);
    this.idleMs = opts.idleMs ?? (Number(process.env.ATELIER_IDLE_MS) || 10 * 60_000);
    this.maxActive = opts.maxActive ?? (Number(process.env.ATELIER_MAX_ACTIVE) || 3);
  }

  async init(): Promise<void> {
    await this.store.load();
    this.emit.host(await this.host.connected());
  }

  hostStatus(): Promise<boolean> {
    return this.host.connected();
  }

  /** Persisted (dormant) sessions merged with any live ones (live status wins). */
  listSessions(): SessionSummary[] {
    const map = new Map<string, SessionSummary>();
    for (const st of this.store.list()) {
      // On launch nothing has a live loop, so a stored "active" is really dormant.
      map.set(st.appId, {
        appId: st.appId,
        title: st.title,
        folder: st.folder,
        status: "inactive",
        updatedAt: st.updatedAt,
        transcript: st.transcript ?? [],
      });
    }
    for (const s of this.live.values()) {
      map.set(s.appId, {
        appId: s.appId,
        title: s.title,
        folder: s.folder,
        status: s.status,
        updatedAt: s.lastActivity,
        transcript: s.transcript,
      });
    }
    return [...map.values()].sort((a, b) => b.updatedAt - a.updatedAt);
  }

  async openSession(folder: string): Promise<string> {
    const apiKey = process.env.ANTHROPIC_API_KEY;
    if (!apiKey) throw new Error("ANTHROPIC_API_KEY is not set");
    await this.ensureVM();

    const appId = "s" + crypto.randomBytes(5).toString("hex");
    const s: LiveSession = {
      appId,
      folder,
      title: path.basename(folder) || folder,
      tag: appId,
      guestPath: `/sessions/${appId}`,
      status: "starting",
      sdkSessionId: "",
      stdoutBuf: "",
      transcript: [],
      lastActivity: Date.now(),
      intentionalStop: false,
    };
    this.live.set(appId, s);
    this.setStatus(s, "starting");

    await this.host.attachWorkspace({ id: this.vmId, path: folder, target: s.guestPath, tag: s.tag });
    await this.startWatcher(s);
    // Seed the guest clock before the loop's first model call: TLS fails if the
    // guest is still at 1970 (no RTC under VZ). The broker also resyncs every 30s.
    await this.host.setTime(this.vmId);
    this.startLoop(s, apiKey);

    await this.store.save({ appId, folder, title: s.title, sdkSessionId: "", transcript: [], status: "active", updatedAt: Date.now() });
    s.status = "active";
    this.setStatus(s, "active");
    this.bumpActivity(s);
    await this.enforceCap(appId);
    return appId;
  }

  async sendMessage(appId: string, text: string): Promise<void> {
    let s = this.live.get(appId);
    if (!s || s.status === "inactive") {
      await this.resume(appId);
      s = this.live.get(appId);
    }
    if (!s) throw new Error(`unknown session ${appId}`);
    if (s.status !== "active") throw new Error(`session ${appId} is ${s.status}`);
    this.bumpActivity(s);
    await this.host.execInput({ id: this.vmId, sessionId: appId, data: b64line({ type: "user", text }) });
  }

  async resume(appId: string): Promise<void> {
    const apiKey = process.env.ANTHROPIC_API_KEY;
    if (!apiKey) throw new Error("ANTHROPIC_API_KEY is not set");
    await this.ensureVM();

    let s = this.live.get(appId);
    if (!s) {
      const stored = this.store.get(appId);
      if (!stored) throw new Error(`unknown session ${appId}`);
      s = {
        appId,
        folder: stored.folder,
        title: stored.title,
        tag: appId,
        guestPath: `/sessions/${appId}`,
        status: "resuming",
        sdkSessionId: stored.sdkSessionId,
        stdoutBuf: "",
        transcript: stored.transcript ?? [],
        lastActivity: Date.now(),
        intentionalStop: false,
      };
      this.live.set(appId, s);
    }
    if (s.status === "active" || s.status === "starting") return;

    s.intentionalStop = false;
    this.setStatus(s, "resuming");
    await this.host.attachWorkspace({ id: this.vmId, path: s.folder, target: s.guestPath, tag: s.tag });
    await this.startWatcher(s);
    // Resume after hibernate also re-seeds the clock: the guest may have drifted
    // across host sleep, and the next model call must see a valid wall clock.
    await this.host.setTime(this.vmId);
    this.startLoop(s, apiKey, s.sdkSessionId || undefined);
    await this.store.patch(appId, { status: "active" });
    s.status = "active";
    this.setStatus(s, "active");
    this.bumpActivity(s);
    await this.enforceCap(appId);
  }

  async hibernate(appId: string): Promise<void> {
    const s = this.live.get(appId);
    if (!s || s.status !== "active") return;
    this.setStatus(s, "hibernating");
    s.intentionalStop = true;
    if (s.idleTimer) {
      clearTimeout(s.idleTimer);
      s.idleTimer = undefined;
    }
    try {
      const ctx = await this.exportContext(s, 30_000);
      if (Array.isArray(ctx.transcript)) s.transcript = ctx.transcript;
      if (ctx.sessionId) s.sdkSessionId = ctx.sessionId;
    } catch {
      // Export timed out — keep the last-known transcript + sdkSessionId.
    }
    await this.endRun(s);
    await this.safeDetach(s);
    s.watcher?.dispose();
    s.watcher = undefined;
    await this.store.save({
      appId,
      folder: s.folder,
      title: s.title,
      sdkSessionId: s.sdkSessionId,
      transcript: s.transcript,
      status: "inactive",
      updatedAt: Date.now(),
    });
    s.status = "inactive";
    this.setStatus(s, "inactive");
  }

  async closeSession(appId: string): Promise<void> {
    const s = this.live.get(appId);
    if (s) {
      s.intentionalStop = true;
      if (s.idleTimer) clearTimeout(s.idleTimer);
      if (s.run) {
        try {
          await this.host.execInput({ id: this.vmId, sessionId: appId, data: b64line({ type: "close" }) });
        } catch {
          /* loop may already be gone */
        }
        await this.endRun(s);
      }
      await this.safeDetach(s);
      s.watcher?.dispose();
      this.live.delete(appId);
    }
    await this.store.remove(appId);
  }

  /** Best-effort teardown on app quit: stop loops, detach mounts, stop the VM. */
  async shutdown(): Promise<void> {
    for (const s of this.live.values()) {
      s.intentionalStop = true;
      if (s.idleTimer) clearTimeout(s.idleTimer);
      s.run?.close();
      s.watcher?.dispose();
      await this.safeDetach(s);
    }
    this.live.clear();
    try {
      await this.host.stopVM(this.vmId);
    } catch {
      /* broker may already be down */
    }
  }

  // --- VM bring-up -------------------------------------------------------------

  private ensureVM(): Promise<void> {
    if (!this.vmReady) {
      this.vmReady = this.bringUpVM().catch((e) => {
        this.vmReady = undefined; // allow a retry on the next call
        throw e;
      });
    }
    return this.vmReady;
  }

  private async bringUpVM(): Promise<void> {
    const status = await this.host.getStatus(); // throws if the broker is down
    this.emit.host(true);
    if (status.vmCount === 0) {
      await this.host.createVM({
        id: this.vmId,
        kernelPath: path.join(this.bundleDir, "vmlinuz"),
        initrdPath: path.join(this.bundleDir, "initrd"),
        rootfsPath: path.join(this.bundleDir, this.rootfsName),
        guestdImagePath: path.join(this.bundleDir, this.guestdImageName),
        memoryMB: 0,
        cpuCount: 0,
      });
      await this.host.startVM(this.vmId);
      await this.waitForGuest();
    }
    await this.host.setEgressPolicy(this.egressAllow);
  }

  private async waitForGuest(): Promise<void> {
    const deadline = Date.now() + this.bootTimeoutMs;
    for (;;) {
      try {
        const run = this.host.execStream({ id: this.vmId, cmd: "node", args: ["--version"] }, () => {});
        const r = await Promise.race([run.result, sleep(5000).then(() => null)]);
        if (r && r.exitCode === 0) return;
        run.close();
      } catch {
        /* guest not up yet */
      }
      if (Date.now() > deadline) throw new Error("guest did not become ready before boot timeout");
      await sleep(2000);
    }
  }

  // --- loop plumbing -----------------------------------------------------------

  private startLoop(s: LiveSession, apiKey: string, resume?: string): void {
    const args = [GUEST_AGENT, "--serve", "--workspace", s.guestPath];
    if (resume) args.push("--resume", resume);
    const env: Record<string, string> = {
      ANTHROPIC_API_KEY: apiKey,
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1",
      DISABLE_AUTOUPDATER: "1",
      DISABLE_TELEMETRY: "1",
      DISABLE_ERROR_REPORTING: "1",
      HOME: "/root",
    };
    for (const k of ["ATELIER_MODEL", "ANTHROPIC_BASE_URL"]) {
      const v = process.env[k];
      if (v) env[k] = v;
    }
    const run = this.host.execStream(
      { id: this.vmId, cmd: GUEST_TSX, args, cwd: GUEST_CWD, env, sessionId: s.appId },
      (stream, data) => this.onOutput(s, stream, data),
    );
    s.run = run;
    run.result.then(
      (r) => this.onLoopEnd(s, r.exitCode, undefined),
      (e) => this.onLoopEnd(s, undefined, e),
    );
  }

  private onOutput(s: LiveSession, stream: OutputStream, data: Buffer): void {
    if (stream === "stderr") return; // guest loop diagnostics; not part of the wire
    s.stdoutBuf += data.toString("utf8");
    for (;;) {
      const nl = s.stdoutBuf.indexOf("\n");
      if (nl < 0) break;
      const line = s.stdoutBuf.slice(0, nl).trim();
      s.stdoutBuf = s.stdoutBuf.slice(nl + 1);
      if (!line) continue;
      let ev: LoopEvent;
      try {
        ev = JSON.parse(line) as LoopEvent;
      } catch {
        continue;
      }
      this.handleEvent(s, ev);
    }
  }

  private handleEvent(s: LiveSession, ev: LoopEvent): void {
    if (ev.type === "init") {
      // init repeats per turn with a stable sessionId — capture idempotently.
      if (ev.sessionId && ev.sessionId !== s.sdkSessionId) {
        s.sdkSessionId = ev.sessionId;
        void this.store.patch(s.appId, { sdkSessionId: ev.sessionId });
      }
      return;
    }
    if (ev.type === "context") {
      s.exportWaiter?.resolve(ev);
      s.exportWaiter = undefined;
      return;
    }
    if (RENDERABLE.has(ev.type)) s.transcript.push(ev);
    this.emit.event(s.appId, ev);
    if (ev.type === "turn_done") {
      void this.store.patch(s.appId, { transcript: s.transcript });
      void s.watcher?.refresh();
    }
  }

  private onLoopEnd(s: LiveSession, code: number | undefined, err: unknown): void {
    s.run = undefined;
    if (s.intentionalStop || s.status === "hibernating" || s.status === "inactive") return;
    // Unexpected exit while we believed it active.
    s.status = "error";
    this.emit.event(s.appId, { type: "error", message: err ? String(err) : `agent loop exited (code ${code ?? "?"})` });
    this.setStatus(s, "error");
    void this.store.patch(s.appId, { status: "inactive" });
  }

  private exportContext(s: LiveSession, timeoutMs: number): Promise<Extract<LoopEvent, { type: "context" }>> {
    return new Promise((resolve, reject) => {
      const t = setTimeout(() => {
        s.exportWaiter = undefined;
        reject(new Error("export_context timed out"));
      }, timeoutMs);
      s.exportWaiter = {
        resolve: (ev) => {
          clearTimeout(t);
          resolve(ev);
        },
        reject: (e) => {
          clearTimeout(t);
          reject(e);
        },
      };
      void this.host.execInput({ id: this.vmId, sessionId: s.appId, data: b64line({ type: "export_context" }) });
    });
  }

  private async endRun(s: LiveSession): Promise<void> {
    const run = s.run;
    if (!run) return;
    // The loop closes its own input after export and exits; wait briefly then force.
    await Promise.race([run.result.catch(() => undefined), sleep(5000)]);
    run.close();
    s.run = undefined;
  }

  private async enforceCap(exceptId: string): Promise<void> {
    for (;;) {
      const active = [...this.live.values()].filter((x) => x.status === "active");
      if (active.length <= this.maxActive) return;
      active.sort((a, b) => a.lastActivity - b.lastActivity); // least-recently-used first
      const victim = active.find((x) => x.appId !== exceptId);
      if (!victim) return;
      await this.hibernate(victim.appId);
    }
  }

  private async startWatcher(s: LiveSession): Promise<void> {
    const w = new WorkspaceWatcher(s.folder, (u) => this.emit.files(s.appId, u));
    s.watcher = w;
    await w.start();
  }

  private async safeDetach(s: LiveSession): Promise<void> {
    try {
      await this.host.detachWorkspace({ id: this.vmId, tag: s.tag });
    } catch {
      /* mount may already be gone */
    }
  }

  private bumpActivity(s: LiveSession): void {
    s.lastActivity = Date.now();
    if (s.idleTimer) clearTimeout(s.idleTimer);
    s.idleTimer = setTimeout(() => void this.hibernate(s.appId).catch(() => undefined), this.idleMs);
  }

  private setStatus(s: LiveSession, status: SessionLifecycle): void {
    s.status = status;
    this.emit.status(s.appId, status, { title: s.title, folder: s.folder });
  }
}
