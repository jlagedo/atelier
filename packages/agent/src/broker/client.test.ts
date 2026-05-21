import net from "node:net";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { BrokerClient, BrokerError, encodeFrame, parseFrames } from "./client";

describe("Content-Length codec", () => {
  it("round-trips a single frame", () => {
    const msg = { jsonrpc: "2.0", id: 1, method: "getStatus" };
    const { messages, rest } = parseFrames(encodeFrame(msg));
    expect(messages).toEqual([msg]);
    expect(rest.length).toBe(0);
  });

  it("parses multiple frames concatenated in one chunk", () => {
    const a = { jsonrpc: "2.0", id: 1, result: { ok: true } };
    const b = { jsonrpc: "2.0", method: "exec/output", params: { stream: "stdout", data: "aGk=" } };
    const { messages, rest } = parseFrames(Buffer.concat([encodeFrame(a), encodeFrame(b)]));
    expect(messages).toEqual([a, b]);
    expect(rest.length).toBe(0);
  });

  it("leaves a partial frame in rest until the body fully arrives", () => {
    const full = encodeFrame({ jsonrpc: "2.0", id: 7, result: 42 });
    const split = full.subarray(0, full.length - 5);
    const first = parseFrames(split);
    expect(first.messages).toEqual([]);
    expect(first.rest.length).toBe(split.length);

    const second = parseFrames(Buffer.concat([first.rest, full.subarray(full.length - 5)]));
    expect(second.messages).toEqual([{ jsonrpc: "2.0", id: 7, result: 42 }]);
    expect(second.rest.length).toBe(0);
  });

  it("preserves multi-byte UTF-8 bodies via byte-length framing", () => {
    const msg = { jsonrpc: "2.0", id: 2, result: "héllo — 世界" };
    const { messages } = parseFrames(encodeFrame(msg));
    expect(messages).toEqual([msg]);
  });
});

describe("BrokerClient over a loopback pipe", () => {
  let server: net.Server | undefined;
  let client: BrokerClient | undefined;

  afterEach(async () => {
    client?.close();
    await new Promise<void>((resolve) => (server ? server.close(() => resolve()) : resolve()));
    server = undefined;
    client = undefined;
  });

  function pipeAddress(): string {
    return process.platform === "win32"
      ? String.raw`\\.\pipe\atelier-test-` + Math.random().toString(36).slice(2)
      : path.join(os.tmpdir(), `atelier-test-${Math.random().toString(36).slice(2)}.sock`);
  }

  it("delivers notifications before resolving the response", async () => {
    const addr = pipeAddress();
    const order: string[] = [];

    server = net.createServer((sock) => {
      let buf: Buffer = Buffer.alloc(0);
      sock.on("data", (chunk) => {
        buf = Buffer.concat([buf, chunk]);
        const { messages, rest } = parseFrames(buf);
        buf = rest;
        for (const m of messages) {
          const req = m as { id: number; method: string };
          if (req.method !== "exec") continue;
          // Two output notifications, then the exit-code response.
          sock.write(encodeFrame({ jsonrpc: "2.0", method: "exec/output", params: { stream: "stdout", data: Buffer.from("one").toString("base64") } }));
          sock.write(encodeFrame({ jsonrpc: "2.0", method: "exec/output", params: { stream: "stderr", data: Buffer.from("two").toString("base64") } }));
          sock.write(encodeFrame({ jsonrpc: "2.0", id: req.id, result: { exitCode: 0 } }));
        }
      });
    });
    await new Promise<void>((resolve) => server!.listen(addr, resolve));

    client = new BrokerClient(addr);
    await client.ready();

    const res = await client.exec(
      { id: "vm0", cmd: "echo", args: [], cwd: "", env: {} },
      (stream, data) => order.push(`${stream}:${data.toString("utf8")}`),
    );

    expect(order).toEqual(["stdout:one", "stderr:two"]);
    expect(res.exitCode).toBe(0);
  });

  it("rejects with a BrokerError on an error response", async () => {
    const addr = pipeAddress();
    server = net.createServer((sock) => {
      let buf: Buffer = Buffer.alloc(0);
      sock.on("data", (chunk) => {
        buf = Buffer.concat([buf, chunk]);
        const { messages, rest } = parseFrames(buf);
        buf = rest;
        for (const m of messages) {
          const req = m as { id: number };
          sock.write(encodeFrame({ jsonrpc: "2.0", id: req.id, error: { code: -32602, message: "bad params" } }));
        }
      });
    });
    await new Promise<void>((resolve) => server!.listen(addr, resolve));

    client = new BrokerClient(addr);
    await client.ready();

    await expect(client.getStatus()).rejects.toBeInstanceOf(BrokerError);
  });

  it("base64-decodes readFile content", async () => {
    const addr = pipeAddress();
    server = net.createServer((sock) => {
      let buf: Buffer = Buffer.alloc(0);
      sock.on("data", (chunk) => {
        buf = Buffer.concat([buf, chunk]);
        const { messages, rest } = parseFrames(buf);
        buf = rest;
        for (const m of messages) {
          const req = m as { id: number; params: { path: string } };
          const content = Buffer.from("name,total\nA,3\n").toString("base64");
          sock.write(encodeFrame({ jsonrpc: "2.0", id: req.id, result: { path: req.params.path, content, size: 15 } }));
        }
      });
    });
    await new Promise<void>((resolve) => server!.listen(addr, resolve));

    client = new BrokerClient(addr);
    await client.ready();

    const buf = await client.readFile("/workspace/orders.csv");
    expect(buf.toString("utf8")).toBe("name,total\nA,3\n");
  });
});
