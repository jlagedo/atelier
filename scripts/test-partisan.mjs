#!/usr/bin/env node
// Test battery for the partisan in-guest agent (`npm run test:partisan`).
//
// Default run is deterministic and API-key-free: it drives partisan with a fake model
// (litellm mock_response) so streaming / tool / interrupt / resume all run offline.
//   1. pytest  — packages/partisan unit + component tests (via `uv`).
//   2. wire    — apps/desktop vitest that spawns the REAL partisan over a pipe and drives
//                it with the SHIPPED PartisanClient (cross-language contract check).
// The real-VM/broker path stays in `npm run e2e:host`.
//
// Mirrors e2e-host.mjs: zero-dep Node, the same logging helpers, a test() tally, exit 1
// if any test fails.
//
// Usage:
//   node scripts/test-partisan.mjs                 pytest + cross-language wire (fake model)
//   node scripts/test-partisan.mjs --pytest-only   only the Python suite
//   node scripts/test-partisan.mjs --wire-only     only the cross-language wire test
//   node scripts/test-partisan.mjs --live          + real-API smoke (needs ANTHROPIC_API_KEY)
//   node scripts/test-partisan.mjs --keep          keep temp workspaces for debugging

import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const partisanDir = path.join(repoRoot, "packages", "partisan");
const desktopDir = path.join(repoRoot, "apps", "desktop");
const isWin = process.platform === "win32";

// ----- args ---------------------------------------------------------------------------------------

const flags = { pytestOnly: false, wireOnly: false, live: false, keep: false };
for (const a of process.argv.slice(2)) {
  if (a === "--pytest-only") flags.pytestOnly = true;
  else if (a === "--wire-only") flags.wireOnly = true;
  else if (a === "--live") flags.live = true;
  else if (a === "--keep") flags.keep = true;
  else if (a === "-h" || a === "--help") {
    const header = fs.readFileSync(fileURLToPath(import.meta.url), "utf8").split("\n");
    console.log(header.slice(1, 22).map((l) => l.replace(/^\/\/ ?/, "")).join("\n"));
    process.exit(0);
  } else die(`unknown flag: ${a} (try --help)`);
}

// ----- tiny helpers (same palette as e2e-host.mjs) ------------------------------------------------

const t0 = Date.now();
const elapsed = () => `${((Date.now() - t0) / 1000).toFixed(0)}s`;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function section(title) {
  console.log(`\n\x1b[1;36m=== ${title} \x1b[0;90m(+${elapsed()})\x1b[0m`);
}
function info(msg) {
  console.log(`\x1b[90m  ${msg}\x1b[0m`);
}
function warn(msg) {
  console.log(`\x1b[33m  ! ${msg}\x1b[0m`);
}
function die(msg) {
  console.error(`\x1b[31mtest-partisan: ${msg}\x1b[0m`);
  process.exit(1);
}
const tail = (s, n = 25) => (s || "").trim().split("\n").slice(-n).join("\n");

// run a command to completion, streaming output; resolves with the exit code.
function run(cmd, args, opts = {}) {
  console.log(`\x1b[90m  $ ${[cmd, ...args].join(" ")}\x1b[0m`);
  return new Promise((resolve) => {
    const child = spawn(cmd, args, { cwd: opts.cwd || repoRoot, env: { ...process.env, ...opts.env }, stdio: ["inherit", "pipe", "pipe"], shell: !!opts.shell });
    let out = "";
    child.stdout.on("data", (d) => {
      out += d;
      process.stdout.write(d);
    });
    child.stderr.on("data", (d) => {
      out += d;
      process.stderr.write(d);
    });
    child.on("error", (e) => resolve({ status: 1, out: `could not launch ${cmd}: ${e.message}` }));
    child.on("close", (code) => resolve({ status: code ?? 1, out }));
  });
}

// ----- tally --------------------------------------------------------------------------------------

const pass = [];
const fail = [];
const skipped = [];
const SKIP = Symbol("skip");
async function test(name, fn) {
  try {
    const note = await fn();
    if (note === SKIP) {
      skipped.push(name);
      console.log(`  \x1b[33m⊘ ${name} — skipped\x1b[0m`);
      return;
    }
    pass.push(name);
    console.log(`  \x1b[32m✅ ${name}\x1b[0m${note ? `\x1b[90m — ${note}\x1b[0m` : ""}`);
  } catch (e) {
    fail.push(name);
    console.log(`  \x1b[31m❌ ${name} — ${e.message}\x1b[0m`);
  }
}

// ----- live smoke driver (real cli_guest, real model) ---------------------------------------------

function spawnLiveAgent(workdir) {
  const child = spawn("uv", ["run", "python", "cli_guest.py", "--serve", "--workspace", workdir], {
    cwd: partisanDir,
    env: process.env,
    stdio: ["pipe", "pipe", "pipe"],
  });
  const events = [];
  let buf = "";
  child.stdout.on("data", (d) => {
    buf += d.toString("utf8");
    for (;;) {
      const nl = buf.indexOf("\n");
      if (nl < 0) break;
      const line = buf.slice(0, nl).trim();
      buf = buf.slice(nl + 1);
      if (!line) continue;
      try {
        events.push(JSON.parse(line));
      } catch {
        /* ignore non-JSON */
      }
    }
  });
  child.stderr.on("data", () => {});
  return {
    events,
    send: (o) => child.stdin.write(JSON.stringify(o) + "\n"),
    waitFor: async (pred, ms = 90_000) => {
      const dl = Date.now() + ms;
      while (Date.now() < dl) {
        if (events.some(pred)) return true;
        await sleep(50);
      }
      return false;
    },
    kill: () => child.kill("SIGKILL"),
  };
}

const isType = (t) => (e) => e.type === t;

async function liveSmoke(work) {
  await test("live: streaming turn", async () => {
    const a = spawnLiveAgent(work);
    try {
      a.send({ type: "user", text: "Reply with a short one-sentence greeting." });
      if (!(await a.waitFor(isType("turn_done")))) throw new Error("no turn_done within timeout");
      if (!a.events.some(isType("text_delta"))) throw new Error("no text_delta (streaming not observed)");
      const r = a.events.find(isType("result"));
      if (!r || r.subtype !== "success") throw new Error(`result subtype=${r?.subtype}`);
      return "streamed + finished";
    } finally {
      a.kill();
    }
  });

  await test("live: interrupt mid-stream", async () => {
    const a = spawnLiveAgent(work);
    try {
      a.send({ type: "user", text: "Count slowly from 1 to 100, one number per line, with a short note on each." });
      if (!(await a.waitFor(isType("text_delta"), 60_000))) return SKIP; // model finished/blocked before any delta
      a.send({ type: "interrupt" });
      if (!(await a.waitFor(isType("turn_done"), 30_000))) throw new Error("no turn_done after interrupt");
      const r = a.events.find(isType("result"));
      if (r?.subtype !== "interrupted") throw new Error(`expected interrupted, got ${r?.subtype}`);
      return "cancelled mid-stream";
    } finally {
      a.kill();
    }
  });
}

// ----- main ---------------------------------------------------------------------------------------

async function main() {
  section("Preflight");
  const uv = spawnSync("uv", ["--version"], { encoding: "utf8" });
  if (uv.status !== 0) die("`uv` not found on PATH — install it (https://docs.astral.sh/uv/) to run partisan tests");
  info((uv.stdout || "").trim());
  info("uv sync (packages/partisan)");
  const sync = await run("uv", ["sync"], { cwd: partisanDir });
  if (sync.status !== 0) die("uv sync failed");

  const work = fs.mkdtempSync(path.join(os.tmpdir(), "partisan-test-"));

  if (!flags.wireOnly) {
    section("pytest (fake model)");
    await test("pytest", async () => {
      const r = spawnSync("uv", ["run", "pytest", "-q"], { cwd: partisanDir, encoding: "utf8", env: process.env });
      if (r.status !== 0) throw new Error(`pytest failed\n${tail(r.stdout)}\n${tail(r.stderr)}`);
      const m = (r.stdout || "").match(/(\d+) passed/);
      return m ? `${m[1]} passed` : "ok";
    });
  }

  if (!flags.pytestOnly) {
    section("cross-language wire (real partisan ↔ PartisanClient)");
    await test("wire", async () => {
      const r = await run(isWin ? "npm.cmd" : "npm", ["--prefix", desktopDir, "run", "test:wire"], {
        env: { PARTISAN_WIRE: "1", PARTISAN_DIR: partisanDir },
        shell: isWin,
      });
      if (r.status !== 0) throw new Error("vitest wire suite failed");
      return "real partisan over a pipe";
    });
  }

  if (flags.live) {
    section("live smoke (real API)");
    if (!process.env.ANTHROPIC_API_KEY && !process.env.LLM_API_KEY) {
      die("--live needs ANTHROPIC_API_KEY (or LLM_API_KEY) in the environment");
    }
    await liveSmoke(work);
  }

  if (!flags.keep) fs.rmSync(work, { recursive: true, force: true });

  section("Summary");
  for (const n of pass) console.log(`  \x1b[32m✅ ${n}\x1b[0m`);
  for (const n of skipped) console.log(`  \x1b[33m⊘ ${n}\x1b[0m`);
  for (const n of fail) console.log(`  \x1b[31m❌ ${n}\x1b[0m`);
  console.log(`\n  ${pass.length} passed, ${fail.length} failed, ${skipped.length} skipped  \x1b[90m(+${elapsed()})\x1b[0m`);
  if (fail.length) {
    console.log("\n\x1b[31mRESULT: FAIL\x1b[0m");
    process.exit(1);
  }
  console.log("\n\x1b[32mRESULT: PASS\x1b[0m");
}

main().catch((e) => {
  console.error(`\x1b[31mtest-partisan: ${e.stack || e.message}\x1b[0m`);
  process.exit(1);
});
