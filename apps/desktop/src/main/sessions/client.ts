// PartisanClient — the transport-agnostic in-guest agent wire client. Owns the NDJSON
// codec (buffer + split on \n + JSON.parse) and the protocol (control sends + the
// export_context request/response correlation). It deliberately does NOT own session
// state, transcript persistence, or RENDERABLE filtering — those stay with the caller,
// which receives every decoded event via hooks.onEvent. This is the code the desktop
// app ships AND the code the cross-language test drives, so there is no drift.

import type { LoopEvent } from "../host-client/types";
import type { LoopOutputStream, LoopTransport, LoopTransportFactory } from "./transport";

export interface PartisanClientHooks {
  /** Every decoded event except `context` (consumed by exportContext). */
  onEvent?: (ev: LoopEvent) => void;
  /** Raw stderr text from the agent process (not part of the JSON wire). */
  onStderr?: (text: string) => void;
  /** A stdout line that failed to parse as JSON. */
  onMalformed?: (line: string) => void;
}

type ContextEvent = Extract<LoopEvent, { type: "context" }>;

export class PartisanClient {
  private readonly transport: LoopTransport;
  private buf = "";
  private exportWaiter?: { resolve: (ev: ContextEvent) => void; reject: (e: unknown) => void };

  constructor(
    factory: LoopTransportFactory,
    private readonly hooks: PartisanClientHooks = {},
  ) {
    this.transport = factory((stream, data) => this.onOutput(stream, data));
  }

  /** Resolves with the agent's exit code when it ends. */
  get done(): Promise<{ exitCode: number }> {
    return this.transport.done;
  }

  user(text: string): Promise<void> | void {
    return this.transport.send({ type: "user", text });
  }

  interrupt(): Promise<void> | void {
    return this.transport.send({ type: "interrupt" });
  }

  pause(): Promise<void> | void {
    return this.transport.send({ type: "pause" });
  }

  /** Graceful stop: the agent finishes any in-flight turn, then exits. */
  close(): Promise<void> | void {
    return this.transport.send({ type: "close" });
  }

  /** Hard-abort the underlying transport. */
  abort(): void {
    this.transport.close();
  }

  exportContext(timeoutMs: number): Promise<ContextEvent> {
    return new Promise<ContextEvent>((resolve, reject) => {
      const t = setTimeout(() => {
        this.exportWaiter = undefined;
        reject(new Error("export_context timed out"));
      }, timeoutMs);
      this.exportWaiter = {
        resolve: (ev) => {
          clearTimeout(t);
          resolve(ev);
        },
        reject: (e) => {
          clearTimeout(t);
          reject(e);
        },
      };
      void Promise.resolve(this.transport.send({ type: "export_context" })).catch((e) => {
        if (this.exportWaiter) {
          this.exportWaiter.reject(e);
          this.exportWaiter = undefined;
        }
      });
    });
  }

  private onOutput(stream: LoopOutputStream, data: Buffer): void {
    if (stream === "stderr") {
      this.hooks.onStderr?.(data.toString("utf8"));
      return;
    }
    this.buf += data.toString("utf8");
    for (;;) {
      const nl = this.buf.indexOf("\n");
      if (nl < 0) break;
      const line = this.buf.slice(0, nl).trim();
      this.buf = this.buf.slice(nl + 1);
      if (!line) continue;
      let ev: LoopEvent;
      try {
        ev = JSON.parse(line) as LoopEvent;
      } catch {
        this.hooks.onMalformed?.(line);
        continue;
      }
      this.handleEvent(ev);
    }
  }

  private handleEvent(ev: LoopEvent): void {
    if (ev.type === "context") {
      this.exportWaiter?.resolve(ev);
      this.exportWaiter = undefined;
      return;
    }
    this.hooks.onEvent?.(ev);
  }
}
