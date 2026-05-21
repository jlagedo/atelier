// Hop 2 (design.md §8): the TypeScript half of the broker client. Speaks JSON-RPC
// 2.0 over the host named pipe with LSP-style Content-Length framing — the exact
// wire contract the Go `services/cmd/vmctl` client uses. exec streams the guest's
// stdout/stderr as `exec/output` notifications (base64) before returning an exit
// code; file content travels base64 so binary files survive the JSON wire.

import net from "node:net";
import {
  RpcMethod,
  type AttachWorkspaceParams,
  type ExecParams,
  type ExecResult,
  type FileContent,
  type ReadFileParams,
  type Status,
  type WriteFileParams,
} from "../protocol";

/** The broker's named pipe (design.md §8 — Hop 2). */
export const DEFAULT_PIPE = String.raw`\\.\pipe\atelier-host`;

interface RpcRequest {
  jsonrpc: "2.0";
  id: number;
  method: string;
  params?: unknown;
}

interface RpcResponse {
  jsonrpc: "2.0";
  id: number;
  result?: unknown;
  error?: RpcError;
}

interface RpcNotification {
  jsonrpc: "2.0";
  method: string;
  params?: unknown;
}

export interface RpcError {
  code: number;
  message: string;
  data?: unknown;
}

/** A JSON-RPC error returned by the broker. */
export class BrokerError extends Error {
  readonly code: number;
  readonly data?: unknown;
  constructor(e: RpcError) {
    super(e.message);
    this.name = "BrokerError";
    this.code = e.code;
    this.data = e.data;
  }
}

/** Called for each notification (no id) that arrives while a call is in flight. */
export type NotifyHandler = (method: string, params: unknown) => void;

// --- codec (exported for unit tests) -------------------------------------------

const HEADER_SEP = "\r\n\r\n";

/** Encode one JSON-RPC message with Content-Length framing. */
export function encodeFrame(msg: unknown): Buffer {
  const body = Buffer.from(JSON.stringify(msg), "utf8");
  const header = Buffer.from(`Content-Length: ${body.length}${HEADER_SEP}`, "ascii");
  return Buffer.concat([header, body]);
}

/**
 * Pull as many complete frames as are buffered; return the parsed messages plus
 * the unconsumed tail (a partial frame still arriving). Pure and synchronous so
 * the framing can be unit-tested without a socket.
 */
export function parseFrames(buf: Buffer): { messages: unknown[]; rest: Buffer } {
  const messages: unknown[] = [];
  let offset = 0;
  for (;;) {
    const headerEnd = buf.indexOf(HEADER_SEP, offset, "ascii");
    if (headerEnd === -1) break;
    const header = buf.toString("ascii", offset, headerEnd);
    const m = /Content-Length:\s*(\d+)/i.exec(header);
    if (!m) {
      // Malformed header: skip past it so the stream doesn't wedge.
      offset = headerEnd + HEADER_SEP.length;
      continue;
    }
    const len = Number(m[1]);
    const bodyStart = headerEnd + HEADER_SEP.length;
    const bodyEnd = bodyStart + len;
    if (bodyEnd > buf.length) break; // body still arriving
    messages.push(JSON.parse(buf.toString("utf8", bodyStart, bodyEnd)));
    offset = bodyEnd;
  }
  return { messages, rest: buf.subarray(offset) };
}

// --- client --------------------------------------------------------------------

interface Pending {
  resolve: (value: unknown) => void;
  reject: (err: unknown) => void;
  onNotify?: NotifyHandler;
}

/**
 * A minimal JSON-RPC client over the broker pipe. One persistent connection
 * carries many calls; `callStream` additionally delivers interleaved
 * notifications. Concurrent streaming calls are not needed (the SDK drives tools
 * one at a time) and are not supported — notifications fan out to any in-flight
 * stream handler, of which there is at most one.
 */
export class BrokerClient {
  private readonly socket: net.Socket;
  private buf: Buffer = Buffer.alloc(0);
  private nextId = 1;
  private readonly pending = new Map<number, Pending>();
  private readonly connected: Promise<void>;
  private closed = false;

  constructor(address: string = process.env.ATELIER_HOST_PIPE || DEFAULT_PIPE) {
    this.socket = net.createConnection({ path: address });
    this.socket.on("data", (chunk) => this.onData(chunk));
    this.socket.on("error", (err) => this.failAll(err));
    this.socket.on("close", () => {
      if (!this.closed) this.failAll(new Error("broker connection closed"));
    });
    this.connected = new Promise((resolve, reject) => {
      this.socket.once("connect", resolve);
      this.socket.once("error", reject);
    });
  }

  /** Resolves once the pipe is connected; rejects with the connect error. */
  ready(): Promise<void> {
    return this.connected;
  }

  close(): void {
    this.closed = true;
    this.socket.end();
  }

  call<T = unknown>(method: string, params?: unknown): Promise<T> {
    return this.send<T>(method, params);
  }

  callStream<T = unknown>(method: string, params: unknown, onNotify: NotifyHandler): Promise<T> {
    return this.send<T>(method, params, onNotify);
  }

  // --- typed method wrappers ---

  exec(p: ExecParams, onOutput: (stream: "stdout" | "stderr", data: Buffer) => void): Promise<ExecResult> {
    return this.callStream<ExecResult>(RpcMethod.exec, p, (method, params) => {
      if (method !== "exec/output") return;
      const o = params as { stream: "stdout" | "stderr"; data: string };
      onOutput(o.stream, Buffer.from(o.data, "base64"));
    });
  }

  async readFile(path: string): Promise<Buffer> {
    const fc = await this.call<FileContent>(RpcMethod.readFile, { path } satisfies ReadFileParams);
    return Buffer.from(fc.content, "base64");
  }

  async writeFile(path: string, content: Buffer | string): Promise<void> {
    const buf = typeof content === "string" ? Buffer.from(content, "utf8") : content;
    await this.call(RpcMethod.writeFile, { path, content: buf.toString("base64") } satisfies WriteFileParams);
  }

  async attachWorkspace(p: AttachWorkspaceParams): Promise<void> {
    await this.call(RpcMethod.attachWorkspace, p);
  }

  getStatus(): Promise<Status> {
    return this.call<Status>(RpcMethod.getStatus);
  }

  // --- internals ---

  private send<T>(method: string, params: unknown, onNotify?: NotifyHandler): Promise<T> {
    const id = this.nextId++;
    const req: RpcRequest = { jsonrpc: "2.0", id, method, params };
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject, onNotify });
      this.socket.write(encodeFrame(req), (err) => {
        if (err) {
          this.pending.delete(id);
          reject(err);
        }
      });
    });
  }

  private onData(chunk: Buffer): void {
    this.buf = Buffer.concat([this.buf, chunk]);
    const { messages, rest } = parseFrames(this.buf);
    this.buf = rest;
    for (const msg of messages) this.dispatch(msg as RpcResponse & RpcNotification);
  }

  private dispatch(msg: RpcResponse & RpcNotification): void {
    // A notification has a method and no id.
    if (msg.method !== undefined && msg.id === undefined) {
      for (const p of this.pending.values()) p.onNotify?.(msg.method, msg.params);
      return;
    }
    const p = this.pending.get(msg.id);
    if (!p) return;
    this.pending.delete(msg.id);
    if (msg.error) p.reject(new BrokerError(msg.error));
    else p.resolve(msg.result);
  }

  private failAll(err: unknown): void {
    for (const p of this.pending.values()) p.reject(err);
    this.pending.clear();
  }
}
