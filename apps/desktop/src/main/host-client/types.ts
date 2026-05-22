// Hop-2 broker protocol types, inlined to avoid a cross-package build dependency
// on the generated `packages/protocol` (gitignored, built by protogen). These
// mirror packages/protocol/schema/protocol.json — keep them in sync by hand. The
// LoopEvent/LoopControl unions mirror the in-guest agent's --serve NDJSON wire
// (packages/agent/src/cli-guest.ts).

export interface Status {
  service: string;
  version: string;
  platform: string;
  pid: number;
  uptimeMs: number;
  vmCount: number;
}

export interface CreateVMParams {
  id: string;
  kernelPath: string;
  initrdPath: string;
  rootfsPath: string;
  memoryMB: number;
  cpuCount: number;
}

export interface ExecParams {
  id: string;
  cmd: string;
  sessionId?: string;
  args?: string[];
  cwd?: string;
  env?: Record<string, string>;
}

export interface ExecResult {
  exitCode: number;
}

export interface ExecInputParams {
  id: string;
  sessionId: string;
  data: string;
}

export interface AttachWorkspaceParams {
  id: string;
  path: string;
  readOnly?: boolean;
  target?: string;
  tag?: string;
  port?: number;
}

export interface DetachWorkspaceParams {
  id: string;
  tag?: string;
}

export interface SetEgressPolicyParams {
  allow: string[];
}

// --- in-guest agent loop wire (--serve NDJSON) ---------------------------------

/** A control message the host writes to the loop's stdin (one JSON per line). */
export type LoopControl =
  | { type: "user"; text: string }
  | { type: "export_context" }
  | { type: "close" };

/** An event the loop writes to its stdout (one JSON per line). */
export type LoopEvent =
  | { type: "init"; sessionId: string }
  | { type: "text"; text: string }
  | { type: "tool_use"; id: string; name: string; input: unknown }
  | { type: "tool_result"; id: string; content: string; isError: boolean }
  | { type: "policy"; door: string; action: string; decision: "allow" | "deny"; reason: string; detail?: string }
  | { type: "result"; subtype: string; result: string }
  | { type: "turn_done" }
  | { type: "context"; sessionId: string; transcript: unknown[] }
  | { type: "error"; message: string };
