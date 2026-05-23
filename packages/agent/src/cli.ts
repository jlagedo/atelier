// S5a.1 — the agent loop on the HOST (Topology A): brain outside, hands inside.
// We host @anthropic-ai/claude-agent-sdk and supply the three seams; the SDK IS
// the loop. Standalone CLI (not welded to Electron) so S5b.1 reuses it verbatim.
//
//   provider seam  → model + env                                      [packages/provider]
//   executeTool    → in-process MCP tools that call the broker        [seams/tools.ts]
//   approvals      → pre-baked policy via the canUseTool callback      [seams/policy.ts]
//
// Run elevated (the broker pipe SD is SYSTEM+Admins), with ANTHROPIC_API_KEY set.

import { parseArgs } from "node:util";
import { query, type SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
import { resolveProvider } from "../../provider/src/index";
import { BrokerClient, DEFAULT_PIPE } from "./broker/client";
import { Policy } from "./seams/policy";
import { createAtelierToolServer } from "./seams/tools";

const SYSTEM_PROMPT = [
  "You operate a sandboxed Linux VM through the atelier tools ONLY.",
  "Use read_file to read inputs and the shell tool to run commands (for example python3) for computation.",
  "You MUST create every output file with the write_file tool — never write output files via shell",
  "redirection or from inside a python script; compute the contents, then call write_file.",
  "The workspace is mounted at /workspace. Do not assume any other tools exist.",
].join(" ");

async function* oneShot(task: string): AsyncGenerator<SDKUserMessage> {
  yield { type: "user", message: { role: "user", content: task } } as SDKUserMessage;
}

function parse() {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      vm: { type: "string", default: "vm0" },
      workspace: { type: "string", default: "/workspace" },
      model: { type: "string" },
      pipe: { type: "string" },
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
    console.error('usage: agent [--vm vm0] [--workspace /workspace] [--model <id>] "<task>"');
    process.exit(2);
  }

  const provider = resolveProvider({ model: values.model });

  const broker = new BrokerClient(values.pipe ?? process.env.ATELIER_HOST_PIPE ?? DEFAULT_PIPE);
  try {
    await broker.ready();
  } catch (e) {
    console.error(
      `Cannot connect to the broker named pipe. Is the host service running, and is this shell elevated?\n  ${(e as Error).message}`,
    );
    process.exit(1);
  }

  const policy = new Policy({
    workspaceRoot: values.workspace as string,
    audit: (e) =>
      console.error(
        `[policy] door=${e.door} action=${e.action} decision=${e.decision}` +
          `${e.path ? ` path=${e.path}` : ""} — ${e.reason}`,
      ),
  });

  const toolServer = createAtelierToolServer(broker, values.vm as string, values.workspace as string);

  let finalText = "";
  let exitCode = 0;
  try {
    for await (const message of query({
      prompt: oneShot(task),
      options: {
        model: provider.model,
        env: { ...process.env, ...provider.env } as Record<string, string>,
        mcpServers: { atelier: toolServer },
        // No allowedTools allowlist on purpose: that would auto-approve and bypass
        // canUseTool. Routing every tool call through canUseTool makes the policy the
        // single chokepoint — it allows the three atelier tools (write gated to the
        // workspace) and denies everything else, auditing each decision.
        canUseTool: async (toolName, input) => {
          const d = policy.evaluate(toolName, input as Record<string, unknown>);
          return d.behavior === "allow"
            ? { behavior: "allow", updatedInput: input }
            : { behavior: "deny", message: d.reason };
        },
        systemPrompt: SYSTEM_PROMPT,
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
  } finally {
    broker.close();
  }

  process.stdout.write("\n");
  if (finalText) console.log(finalText);
  process.exit(exitCode);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
