// S5b.1 — the agent loop INSIDE the guest (Topology B): brain + hands in the cage.
// Same SDK, same provider + policy seams as the host loop (src/cli.ts); only the
// executeTool seam flips. Here the loop already runs in the sandbox, so its "hands"
// are the SDK's BUILT-IN coding tools (Bash/Read/Write/Edit/Glob/Grep/…) acting
// directly on the guest fs — no broker, no MCP, no Hop-2/3 round-trip for tools.
// The model call escapes via the egress jail (the operator allowlists the API host
// before launch); the policy gate (canUseTool) still audits every tool call.
//
// Launched by the host (e.g. `vmctl agent`) over the broker's exec, with
// ANTHROPIC_API_KEY in the process env and the working directory at /workspace.

import { parseArgs } from "node:util";
import { query, type SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
import { resolveProvider } from "../../provider/src/index";
import { Policy } from "./seams/policy";

const SYSTEM_PROMPT = [
  "You operate INSIDE a sandboxed Linux VM. Use your tools (Bash, Read, Write, Edit, Glob, Grep)",
  "to complete the task; they act directly on this VM's filesystem.",
  "The workspace is mounted at /workspace and is your working directory — files you create there are",
  "the deliverables (it is shared back to the host). python3 is available for computation.",
  "You have no network access except the model itself, so do not attempt to fetch from the web.",
].join(" ");

async function* oneShot(task: string): AsyncGenerator<SDKUserMessage> {
  yield { type: "user", message: { role: "user", content: task } } as SDKUserMessage;
}

function parse() {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      workspace: { type: "string", default: "/workspace" },
      model: { type: "string" },
      "max-turns": { type: "string", default: "20" },
      task: { type: "string" },
    },
  });
  const task = (values.task ?? positionals.join(" ")).trim();
  return { values, task };
}

async function main(): Promise<void> {
  const { values, task } = parse();
  if (!task) {
    console.error('usage: cli-guest [--workspace /workspace] [--model <id>] "<task>"');
    process.exit(2);
  }

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

  let finalText = "";
  let exitCode = 0;
  for await (const message of query({
    prompt: oneShot(task),
    options: {
      model: provider.model,
      env: { ...process.env, ...provider.env } as Record<string, string>,
      // The loop is in the cage, so the built-in tools (Bash/Read/Write/Edit/Glob/
      // Grep/…) act on the guest fs — exactly what we want. We do NOT pass an
      // allowedTools allowlist: that would auto-approve and bypass canUseTool, which
      // is the single chokepoint that allows the in-cage coding set (audited) and
      // denies anything that would reach out of the cage.
      canUseTool: async (toolName, input) => {
        const d = policy.evaluate(toolName, input as Record<string, unknown>);
        return d.behavior === "allow"
          ? { behavior: "allow", updatedInput: input }
          : { behavior: "deny", message: d.reason };
      },
      systemPrompt: SYSTEM_PROMPT,
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

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
