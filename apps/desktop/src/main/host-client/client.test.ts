import { describe, expect, it } from "vitest";
import { encodeFrame, parseFrames } from "./client";

describe("host-client codec", () => {
  it("round-trips one framed message", () => {
    const msg = { jsonrpc: "2.0", id: 1, method: "getStatus" };
    const { messages, rest } = parseFrames(encodeFrame(msg));
    expect(messages).toEqual([msg]);
    expect(rest.length).toBe(0);
  });

  it("parses several frames concatenated in one buffer", () => {
    const a = { jsonrpc: "2.0", id: 1, result: { ok: true } };
    const b = { jsonrpc: "2.0", method: "exec/output", params: { stream: "stdout", data: "aGk=" } };
    const buf = Buffer.concat([encodeFrame(a), encodeFrame(b)]);
    const { messages, rest } = parseFrames(buf);
    expect(messages).toEqual([a, b]);
    expect(rest.length).toBe(0);
  });

  it("keeps a partial frame as rest until the body finishes arriving", () => {
    const msg = { jsonrpc: "2.0", id: 7, result: 42 };
    const full = encodeFrame(msg);
    const split = full.length - 3;

    const first = parseFrames(full.subarray(0, split));
    expect(first.messages).toEqual([]);
    expect(first.rest.length).toBe(split);

    const { messages, rest } = parseFrames(Buffer.concat([first.rest, full.subarray(split)]));
    expect(messages).toEqual([msg]);
    expect(rest.length).toBe(0);
  });

  it("handles a header split across chunk boundaries", () => {
    const msg = { jsonrpc: "2.0", id: 9, result: "x" };
    const full = encodeFrame(msg);
    // Cut inside the Content-Length header.
    const first = parseFrames(full.subarray(0, 8));
    expect(first.messages).toEqual([]);
    const { messages } = parseFrames(Buffer.concat([first.rest, full.subarray(8)]));
    expect(messages).toEqual([msg]);
  });

  it("uses the byte length, not character count, for multibyte bodies", () => {
    const msg = { jsonrpc: "2.0", id: 2, result: "café—naïve—日本語" };
    const { messages, rest } = parseFrames(encodeFrame(msg));
    expect(messages).toEqual([msg]);
    expect(rest.length).toBe(0);
  });
});
