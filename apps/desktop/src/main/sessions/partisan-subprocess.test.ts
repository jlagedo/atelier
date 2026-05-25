// @vitest-environment node
//
// Cross-language wire conformance: drive the REAL partisan agent (Python) over a
// subprocess pipe using the SHIPPED PartisanClient. Deterministic via the fake-model
// shim (tests/fake_serve.py + PARTISAN_FAKE_SCRIPT) — no VM, no API key. This proves the
// NDJSON contract between the two programs end to end.
//
// Gated on PARTISAN_WIRE=1 (set by `npm run test:partisan`) so a bare `vitest run`
// never spawns Python. The partisan dir comes from PARTISAN_DIR or a relative fallback.

import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import type { LoopEvent } from "../host-client/types";
import { PartisanClient } from "./client";
import { subprocessTransport } from "./subprocess-transport";

const here = path.dirname(fileURLToPath(import.meta.url));
const PARTISAN_DIR = process.env.PARTISAN_DIR ?? path.resolve(here, "../../../../../packages/partisan");

function uvAvailable(): boolean {
  try {
    return spawnSync("uv", ["--version"], { encoding: "utf8" }).status === 0;
  } catch {
    return false;
  }
}

const enabled = process.env.PARTISAN_WIRE === "1" && existsSync(PARTISAN_DIR) && uvAvailable();

interface Driver {
  client: PartisanClient;
  events: LoopEvent[];
  waitFor: (pred: (e: LoopEvent) => boolean, timeoutMs?: number) => Promise<void>;
}

function startPartisan(script: unknown[]): Driver {
  const work = mkdtempSync(path.join(tmpdir(), "partisan-ws-"));
  const persist = mkdtempSync(path.join(tmpdir(), "partisan-persist-"));
  const scriptPath = path.join(mkdtempSync(path.join(tmpdir(), "partisan-script-")), "script.json");
  writeFileSync(scriptPath, JSON.stringify(script));

  const events: LoopEvent[] = [];
  const client = new PartisanClient(
    subprocessTransport({
      cmd: "uv",
      args: ["run", "python", "-m", "tests.fake_serve", "--serve", "--workspace", work],
      cwd: PARTISAN_DIR,
      env: { PARTISAN_FAKE_SCRIPT: scriptPath, PARTISAN_PERSIST: persist, LLM_API_KEY: "sk-fake" },
    }),
    {
      onEvent: (e) => events.push(e),
      onStderr: () => {},
    },
  );

  const waitFor = async (pred: (e: LoopEvent) => boolean, timeoutMs = 25_000) => {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (events.some(pred)) return;
      await new Promise((r) => setTimeout(r, 25));
    }
    throw new Error(`event not seen in ${timeoutMs}ms; got ${events.map((e) => e.type).join(",")}`);
  };

  return { client, events, waitFor };
}

const isType = (t: string) => (e: LoopEvent) => e.type === t;

describe.skipIf(!enabled)("partisan ↔ PartisanClient over a pipe", () => {
  let driver: Driver | undefined;
  afterEach(() => {
    driver?.client.abort();
    driver = undefined;
  });

  it(
    "streams a text turn (init → text_delta → text → result → turn_done)",
    async () => {
      driver = startPartisan([{ text: "hello world from partisan" }]);
      const { client, events, waitFor } = driver;
      client.user("hi");
      await waitFor(isType("turn_done"));
      const ts = events.map((e) => e.type);
      expect(ts[0]).toBe("init");
      expect(ts).toContain("text_delta");
      expect(ts).toContain("text");
      expect(ts.indexOf("text_delta")).toBeLessThan(ts.indexOf("result"));
      const result = events.find(isType("result")) as Extract<LoopEvent, { type: "result" }>;
      expect(result.subtype).toBe("success");
    },
    30_000,
  );

  it(
    "interrupts a turn mid-stream",
    async () => {
      driver = startPartisan([{ text: "a long streamed answer", stall: 3.0 }]);
      const { client, events, waitFor } = driver;
      client.user("go");
      await waitFor(isType("text_delta")); // gate on observed delta, never a fixed sleep
      client.interrupt();
      await waitFor(isType("turn_done"));
      expect(events.map((e) => e.type)).toContain("interrupted");
      const result = events.find(isType("result")) as Extract<LoopEvent, { type: "result" }>;
      expect(result.subtype).toBe("interrupted");
    },
    30_000,
  );

  it(
    "round-trips export_context and exits cleanly",
    async () => {
      driver = startPartisan([{ text: "exported answer" }]);
      const { client, waitFor } = driver;
      client.user("hi");
      await waitFor(isType("turn_done"));
      const ctx = await client.exportContext(10_000);
      expect(ctx.sessionId).toBeTruthy();
      expect(Array.isArray(ctx.transcript)).toBe(true);
      await expect(client.done).resolves.toEqual({ exitCode: 0 });
    },
    30_000,
  );
});
