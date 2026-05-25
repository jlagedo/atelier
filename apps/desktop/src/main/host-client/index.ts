// Hop 2 facade (design.md §8): the typed surface the Electron main process uses to
// drive the Go host. Per the connection model, every call/run gets its OWN pipe
// connection — the broker's rpc server is concurrent, but the Go rpc.Client it
// uses host→guest is one-in-flight, and (more importantly) a streaming exec fans
// its exec/output notifications to every handler on that connection, so each
// long-lived session run needs an isolated connection to avoid cross-talk.

import { PipeClient } from "./client";
import type {
  AttachWorkspaceParams,
  CreateVMParams,
  DetachWorkspaceParams,
  ExecInputParams,
  ExecParams,
  ExecResult,
  Status,
} from "./types";

/** The broker's host address (design.md §8 — Hop 2): a named pipe on Windows, a unix
 *  socket for macOS/Linux dev. `net.createConnection({ path })` handles both. */
export function defaultHostAddress(platform: NodeJS.Platform = process.platform): string {
  return platform === "win32"
    ? String.raw`\\.\pipe\atelierd`
    : "/tmp/atelierd.sock"; // matches the Go broker dev default (README §"Dev without HCS")
}

export type OutputStream = "stdout" | "stderr";

/** A handle to a (possibly long-lived) exec run on its own connection. */
export interface ExecRun {
  /** Resolves with the guest exit code when the run ends; rejects on abort/error. */
  result: Promise<ExecResult>;
  /** Hard-abort: drop the connection (use execInput {close} for a clean stop). */
  close(): void;
}

export class HostClient {
  constructor(
    private readonly address: string = process.env.ATELIER_ADDR ??
      process.env.ATELIER_PIPE ??
      defaultHostAddress(),
  ) {}

  // withConn opens a fresh connection, runs fn, and always closes it.
  private async withConn<T>(fn: (c: PipeClient) => Promise<T>): Promise<T> {
    const c = new PipeClient(this.address);
    try {
      await c.ready();
      return await fn(c);
    } finally {
      c.close();
    }
  }

  getStatus(): Promise<Status> {
    return this.withConn((c) => c.call<Status>("getStatus"));
  }

  /** True if the broker answers — used for a "host running?" health signal. */
  async connected(): Promise<boolean> {
    try {
      await this.getStatus();
      return true;
    } catch {
      return false;
    }
  }

  createVM(p: CreateVMParams): Promise<void> {
    return this.withConn((c) => c.call<void>("createVM", p));
  }

  startVM(id: string): Promise<void> {
    return this.withConn((c) => c.call<void>("startVM", { id }));
  }

  stopVM(id: string): Promise<void> {
    return this.withConn((c) => c.call<void>("stopVM", { id }));
  }

  attachWorkspace(p: AttachWorkspaceParams): Promise<void> {
    return this.withConn((c) => c.call<void>("attachWorkspace", p));
  }

  detachWorkspace(p: DetachWorkspaceParams): Promise<void> {
    return this.withConn((c) => c.call<void>("detachWorkspace", p));
  }

  setEgressPolicy(allow: string[]): Promise<void> {
    return this.withConn((c) => c.call<void>("setEgressPolicy", { allow }));
  }

  /** Push the host wall clock into the guest (no RTC under VZ → guest boots at 1970). */
  setTime(id: string): Promise<void> {
    return this.withConn((c) => c.call<void>("setTime", { id }));
  }

  /** Push a stdin chunk into a persistent exec session (host→guest input). */
  execInput(p: ExecInputParams): Promise<void> {
    return this.withConn((c) => c.call<void>("execInput", p));
  }

  /**
   * Start an exec run on a dedicated connection, delivering decoded stdout/stderr
   * chunks as they stream. For a persistent loop, pass `sessionId` so execInput can
   * feed it; the run resolves when the loop exits.
   */
  execStream(p: ExecParams, onOutput: (stream: OutputStream, data: Buffer) => void): ExecRun {
    const c = new PipeClient(this.address);
    const result = (async () => {
      await c.ready();
      try {
        return await c.callStream<ExecResult>("exec", p, (method, params) => {
          if (method !== "exec/output") return;
          const o = params as { stream: OutputStream; data: string };
          onOutput(o.stream, Buffer.from(o.data, "base64"));
        });
      } finally {
        c.close();
      }
    })();
    return { result, close: () => c.close() };
  }
}
