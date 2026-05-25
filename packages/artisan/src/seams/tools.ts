// executeTool seam (design.md §8, Topology A): the agent's "hands" are inside the
// guest, so we do NOT give the SDK its built-in host Bash/Read/Write. Instead we
// expose an in-process MCP server whose tools route to the broker over Hop 2
// (→ guest Hop 3). The handlers run in this Node process, so they hold the live
// broker client. The same server is reused verbatim in Topology B (S5b.1); only
// the transport behind the broker client changes.
//
// The broker's Files door is workspace-RELATIVE and rejects absolute paths
// (services/internal/broker/files.go: jailPath). The guest, however, sees the
// share at /workspace. So read_file/write_file accept a guest path the model is
// comfortable with (/workspace/foo or foo) and translate it to the relative form
// the broker expects. The shell tool runs inside the guest, where /workspace is a
// real path, so it needs no translation.

import { createSdkMcpServer, tool } from "@anthropic-ai/claude-agent-sdk";
import { z } from "zod";
import type { BrokerClient } from "../broker/client";
import { normalizePosix, resolveGuestPath } from "./policy";

/** Map a guest path to the workspace-relative path the broker's jail expects.
 *  Paths outside the workspace are returned unchanged so the broker rejects them
 *  with a clear "must be relative" error (defense in depth alongside the policy). */
function toBrokerRel(workspaceRoot: string, p: string): string {
  const abs = resolveGuestPath(workspaceRoot, p);
  const root = normalizePosix(workspaceRoot);
  if (abs === root) throw new Error(`refusing to operate on the workspace root itself: ${p}`);
  if (abs.startsWith(root + "/")) return abs.slice(root.length + 1);
  return p;
}

export function createAtelierToolServer(broker: BrokerClient, vmId: string, workspaceRoot: string) {
  return createSdkMcpServer({
    name: "atelier",
    version: "0.1.0",
    tools: [
      tool(
        "shell",
        "Run a command inside the sandboxed Linux VM and return its combined stdout/stderr and exit status. Use this for computation, e.g. running python3.",
        {
          cmd: z.string().describe('The executable to run, e.g. "python3" or "ls".'),
          args: z.array(z.string()).optional().describe("Arguments passed to the command."),
          cwd: z.string().optional().describe("Working directory inside the VM."),
        },
        async (args) => {
          const chunks: string[] = [];
          const res = await broker.exec(
            { id: vmId, cmd: args.cmd, args: args.args ?? [], cwd: args.cwd ?? "", env: {} },
            (_stream, data) => chunks.push(data.toString("utf8")),
          );
          const output = chunks.join("");
          const text = output.length > 0 ? output : "(no output)";
          return {
            content: [{ type: "text", text: `exit ${res.exitCode}\n${text}` }],
            isError: res.exitCode !== 0,
          };
        },
      ),
      tool(
        "read_file",
        "Read a file from the workspace and return its UTF-8 text contents.",
        {
          path: z.string().describe("Path under the workspace, e.g. /workspace/orders.csv."),
        },
        async (args) => {
          const buf = await broker.readFile(toBrokerRel(workspaceRoot, args.path));
          return { content: [{ type: "text", text: buf.toString("utf8") }] };
        },
      ),
      tool(
        "write_file",
        "Write UTF-8 text to a file in the workspace. Use this for final outputs; it is gated by policy.",
        {
          path: z.string().describe("Path under the workspace, e.g. /workspace/summary.csv."),
          content: z.string().describe("The full file contents to write."),
        },
        async (args) => {
          await broker.writeFile(toBrokerRel(workspaceRoot, args.path), args.content);
          return {
            content: [{ type: "text", text: `Wrote ${Buffer.byteLength(args.content, "utf8")} bytes to ${args.path}.` }],
          };
        },
      ),
    ],
  });
}
