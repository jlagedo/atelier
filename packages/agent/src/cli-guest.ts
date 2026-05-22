// S5b.1 / S6.1 — the agent loop INSIDE the guest (Topology B): brain + hands in
// the cage. Same SDK, same provider + policy seams as the host loop (src/cli.ts);
// only the executeTool seam flips. Here the loop already runs in the sandbox, so
// its "hands" are the SDK's BUILT-IN coding tools (Bash/Read/Write/Edit/Glob/Grep/…)
// acting directly on the guest fs — no broker, no MCP, no Hop-2/3 round-trip for
// tools. The model call escapes via the egress jail (the operator allowlists the
// API host before launch); the policy gate (canUseTool) still audits every call.
//
// Two run modes:
//   - one-shot (default): `--task "<task>"` runs once, prints human-readable
//     output, exits. Drives `vmctl agent`/`exec` — unchanged.
//   - serve (--serve): a PERSISTENT multi-turn loop (S6.1). It reads NDJSON
//     control messages on stdin and emits NDJSON events on stdout, so the host
//     (Electron Session Manager) drives one long-lived in-guest session per WORK
//     session. Context lives in the SDK session (resumable by session_id), so the
//     host can hibernate (kill) and later resume (`--resume <id>`) the loop.
//
// Wire protocol (serve mode), one JSON object per line:
//   stdin  (host → loop):
//     {"type":"user","text":"…"}        a new user turn (feeds the running loop)
//     {"type":"export_context"}          finish any in-flight turn, then emit
//                                        {"type":"context",…} (for hibernate)
//     {"type":"close"}                   end the loop cleanly
//   stdout (loop → host):
//     {"type":"init","sessionId":"…"}                   session id (resume handle)
//     {"type":"text","text":"…"}                        assistant text
//     {"type":"tool_use","id","name","input"}           a tool call
//     {"type":"tool_result","id","content","isError"}   its result
//     {"type":"policy","door","action","decision","reason","detail"}  audited gate
//     {"type":"result","subtype","result"}              a turn's final result
//     {"type":"turn_done"}                              ready for the next turn
//     {"type":"context","sessionId","transcript"}       reply to export_context
//     {"type":"error","message"}                        a loop-level error

import * as readline from "node:readline";
import { parseArgs } from "node:util";
import { query, type SDKMessage, type SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
import { resolveProvider } from "../../provider/src/index";
import { Policy } from "./seams/policy";

function systemPromptFor(workspace: string): string {
  return [
    "You operate INSIDE a sandboxed Linux VM. Use your tools (Bash, Read, Write, Edit, Glob, Grep)",
    "to complete the task; they act directly on this VM's filesystem.",
    `Your workspace is mounted at ${workspace} and is your working directory — files you create there are`,
    "the deliverables (it is shared back to the host). python3 is available for computation.",
    "You have no network access except the model itself, so do not attempt to fetch from the web.",
  ].join(" ");
}

function parse() {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      workspace: { type: "string", default: "/workspace" },
      model: { type: "string" },
      "max-turns": { type: "string", default: "20" },
      task: { type: "string" },
      serve: { type: "boolean", default: false },
      resume: { type: "string" },
    },
  });
  const task = (values.task ?? positionals.join(" ")).trim();
  return { values, task };
}

// AsyncQueue is a single-consumer queue that exposes its items as an async
// iterable. The readline handler pushes user turns onto it as they arrive; the
// SDK's streaming-input prompt drains it. close() ends the iteration (and so the
// query), which is how the loop shuts down or hibernates.
class AsyncQueue<T> {
  private items: T[] = [];
  private waiters: ((r: IteratorResult<T>) => void)[] = [];
  private closed = false;

  push(item: T): void {
    if (this.closed) return;
    const w = this.waiters.shift();
    if (w) w({ value: item, done: false });
    else this.items.push(item);
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    let w: ((r: IteratorResult<T>) => void) | undefined;
    while ((w = this.waiters.shift())) w({ value: undefined as never, done: true });
  }

  async *iterate(): AsyncGenerator<T> {
    while (true) {
      const item = this.items.shift();
      if (item !== undefined) {
        yield item;
        continue;
      }
      if (this.closed) return;
      const next = await new Promise<IteratorResult<T>>((resolve) => this.waiters.push(resolve));
      if (next.done) return;
      yield next.value;
    }
  }
}

function userTurn(text: string): SDKUserMessage {
  return { type: "user", message: { role: "user", content: text } } as SDKUserMessage;
}

// toolResultText flattens a tool_result's content (string | block[]) to a string.
function toolResultText(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((c) => {
        if (typeof c === "string") return c;
        const b = c as { type?: string; text?: string };
        if (b?.type === "text") return b.text ?? "";
        if (b?.type === "image") return "[image]";
        return "";
      })
      .join("");
  }
  return content == null ? "" : String(content);
}

// serve runs the persistent multi-turn loop driven by NDJSON over stdin/stdout.
async function serve(values: ReturnType<typeof parse>["values"]): Promise<void> {
  const provider = resolveProvider({ model: values.model });
  const workspace = values.workspace as string;

  const queue = new AsyncQueue<SDKUserMessage>();
  let sessionId = "";
  let turnActive = false;
  let pendingExport = false;

  // transcript is the host-persistable record of renderable events (what the UI
  // shows), so a hibernated session can be rebuilt on resume even after an app
  // restart. resume itself rides on sessionId (the SDK reloads its own context).
  const transcript: Record<string, unknown>[] = [];
  const RENDERABLE = new Set(["text", "tool_use", "tool_result", "policy", "result"]);
  const emit = (ev: Record<string, unknown>): void => {
    if (RENDERABLE.has(ev.type as string)) transcript.push(ev);
    process.stdout.write(JSON.stringify(ev) + "\n");
  };

  const exportContext = (): void => emit({ type: "context", sessionId, transcript });

  const policy = new Policy({
    workspaceRoot: workspace,
    mode: "guest",
    audit: (e) =>
      emit({ type: "policy", door: e.door, action: e.action, decision: e.decision, reason: e.reason, detail: e.path }),
  });

  const rl = readline.createInterface({ input: process.stdin });
  rl.on("line", (raw) => {
    const line = raw.trim();
    if (!line) return;
    let msg: { type?: string; text?: string };
    try {
      msg = JSON.parse(line);
    } catch {
      emit({ type: "error", message: "stdin: invalid JSON line" });
      return;
    }
    switch (msg.type) {
      case "user":
        turnActive = true;
        queue.push(userTurn(String(msg.text ?? "")));
        break;
      case "export_context":
        // Wait for any in-flight turn to finish so the SDK session is consistent;
        // if idle, export and end now.
        if (turnActive) pendingExport = true;
        else {
          exportContext();
          queue.close();
        }
        break;
      case "close":
        queue.close();
        break;
      default:
        emit({ type: "error", message: `stdin: unknown control "${msg.type}"` });
    }
  });
  rl.on("close", () => queue.close());

  const options: Parameters<typeof query>[0]["options"] = {
    model: provider.model,
    env: { ...process.env, ...provider.env } as Record<string, string>,
    // No allowedTools allowlist: that would auto-approve and bypass canUseTool,
    // the single chokepoint that allows the in-cage coding set (audited) and denies
    // anything reaching out of the cage.
    canUseTool: async (toolName, input) => {
      const d = policy.evaluate(toolName, input as Record<string, unknown>);
      return d.behavior === "allow"
        ? { behavior: "allow", updatedInput: input }
        : { behavior: "deny", message: d.reason };
    },
    systemPrompt: systemPromptFor(workspace),
    cwd: workspace,
    settingSources: [],
  };
  // Resume a prior in-guest session (hibernate→resume): the SDK reloads its full
  // conversation context for sessionId, so the loop continues where it left off.
  if (values.resume) (options as Record<string, unknown>).resume = values.resume;

  try {
    for await (const message of query({ prompt: queue.iterate(), options })) {
      handleMessage(message as SDKMessage);
    }
  } catch (e) {
    emit({ type: "error", message: e instanceof Error ? e.message : String(e) });
    process.exit(1);
  }
  process.exit(0);

  function handleMessage(message: SDKMessage): void {
    switch (message.type) {
      case "system":
        if (message.subtype === "init") {
          sessionId = message.session_id;
          emit({ type: "init", sessionId });
        }
        break;
      case "assistant":
        for (const block of message.message.content) {
          if (block.type === "text") emit({ type: "text", text: block.text });
          else if (block.type === "tool_use")
            emit({ type: "tool_use", id: block.id, name: block.name, input: block.input });
        }
        break;
      case "user": {
        // Tool results arrive as user messages carrying tool_result blocks.
        const content = message.message.content;
        if (Array.isArray(content)) {
          for (const block of content) {
            const b = block as { type?: string; tool_use_id?: string; content?: unknown; is_error?: boolean };
            if (b?.type === "tool_result")
              emit({ type: "tool_result", id: b.tool_use_id, content: toolResultText(b.content), isError: !!b.is_error });
          }
        }
        break;
      }
      case "result":
        turnActive = false;
        emit({
          type: "result",
          subtype: message.subtype,
          result: message.subtype === "success" ? message.result : "",
        });
        emit({ type: "turn_done" });
        if (pendingExport) {
          pendingExport = false;
          exportContext();
          queue.close();
        }
        break;
    }
  }
}

// runOnce is the original one-shot path: run a single task, print human-readable
// output, exit. Keeps `vmctl agent`/`exec` behavior unchanged.
async function runOnce(values: ReturnType<typeof parse>["values"], task: string): Promise<void> {
  const provider = resolveProvider({ model: values.model });
  const workspace = values.workspace as string;

  const policy = new Policy({
    workspaceRoot: workspace,
    mode: "guest",
    audit: (e) =>
      console.error(
        `[policy] door=${e.door} action=${e.action} decision=${e.decision}` +
          `${e.path ? ` path=${e.path}` : ""} — ${e.reason}`,
      ),
  });

  async function* oneShot(): AsyncGenerator<SDKUserMessage> {
    yield userTurn(task);
  }

  let finalText = "";
  let exitCode = 0;
  for await (const message of query({
    prompt: oneShot(),
    options: {
      model: provider.model,
      env: { ...process.env, ...provider.env } as Record<string, string>,
      canUseTool: async (toolName, input) => {
        const d = policy.evaluate(toolName, input as Record<string, unknown>);
        return d.behavior === "allow"
          ? { behavior: "allow", updatedInput: input }
          : { behavior: "deny", message: d.reason };
      },
      systemPrompt: systemPromptFor(workspace),
      cwd: workspace,
      maxTurns: Number(values["max-turns"]),
      settingSources: [],
    },
  })) {
    if (message.type === "assistant") {
      for (const block of message.message.content) {
        if (block.type === "text") process.stdout.write(block.text);
        else if (block.type === "tool_use") console.error(`\n[tool] ${block.name} ${JSON.stringify(block.input)}`);
      }
    } else if (message.type === "result") {
      if (message.subtype === "success") finalText = message.result;
      else {
        exitCode = 1;
        console.error(`\n[result] ${message.subtype}`);
      }
    }
  }

  process.stdout.write("\n");
  if (finalText) console.log(finalText);
  process.exit(exitCode);
}

async function main(): Promise<void> {
  const { values, task } = parse();

  if (values.serve) {
    await serve(values);
    return;
  }

  if (!task) {
    console.error('usage: cli-guest [--workspace /workspace] [--model <id>] "<task>"  (or --serve for the persistent loop)');
    process.exit(2);
  }
  await runOnce(values, task);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
