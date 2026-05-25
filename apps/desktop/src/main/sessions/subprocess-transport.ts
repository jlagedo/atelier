// Test transport for PartisanClient: spawn the agent as a child process and talk over
// its stdio. Control messages are written as RAW NDJSON to stdin (no base64 — that's a
// broker-only detail). Used by the cross-language wire test to drive the real partisan
// agent without a VM.

import { spawn } from "node:child_process";
import type { LoopTransport, LoopTransportFactory } from "./transport";

export interface SubprocessOptions {
  cmd: string;
  args: string[];
  cwd?: string;
  env?: Record<string, string>;
}

export function subprocessTransport(opts: SubprocessOptions): LoopTransportFactory {
  return (onOutput): LoopTransport => {
    const child = spawn(opts.cmd, opts.args, {
      cwd: opts.cwd,
      env: { ...process.env, ...opts.env },
      stdio: ["pipe", "pipe", "pipe"],
    });
    child.stdout.on("data", (d: Buffer) => onOutput("stdout", d));
    child.stderr.on("data", (d: Buffer) => onOutput("stderr", d));

    const done = new Promise<{ exitCode: number }>((resolve, reject) => {
      child.on("exit", (code) => resolve({ exitCode: code ?? 0 }));
      child.on("error", reject);
    });

    return {
      send: (control) => {
        child.stdin.write(JSON.stringify(control) + "\n");
      },
      close: () => {
        child.kill("SIGKILL");
      },
      done,
    };
  };
}
