import { describe, expect, it } from "vitest";
import type { LoopControl, LoopEvent } from "../host-client/types";
import { PartisanClient, type PartisanClientHooks } from "./client";
import type { LoopOutputStream, LoopTransport, LoopTransportFactory } from "./transport";

class MemTransport {
  sent: LoopControl[] = [];
  closed = false;
  private sink!: (stream: LoopOutputStream, data: Buffer) => void;
  private resolveDone!: (r: { exitCode: number }) => void;
  readonly done = new Promise<{ exitCode: number }>((res) => {
    this.resolveDone = res;
  });

  readonly factory: LoopTransportFactory = (onOutput): LoopTransport => {
    this.sink = onOutput;
    return {
      send: (c) => {
        this.sent.push(c);
      },
      close: () => {
        this.closed = true;
      },
      done: this.done,
    };
  };

  out(text: string) {
    this.sink("stdout", Buffer.from(text, "utf8"));
  }
  err(text: string) {
    this.sink("stderr", Buffer.from(text, "utf8"));
  }
  exit(code: number) {
    this.resolveDone({ exitCode: code });
  }
}

function setup(hooks?: PartisanClientHooks) {
  const t = new MemTransport();
  const events: LoopEvent[] = [];
  const malformed: string[] = [];
  const stderr: string[] = [];
  const client = new PartisanClient(t.factory, {
    onEvent: (e) => events.push(e),
    onMalformed: (l) => malformed.push(l),
    onStderr: (s) => stderr.push(s),
    ...hooks,
  });
  return { t, client, events, malformed, stderr };
}

describe("PartisanClient codec", () => {
  it("decodes one line into an event", () => {
    const { t, events } = setup();
    t.out(`${JSON.stringify({ type: "text", text: "hi" })}\n`);
    expect(events).toEqual([{ type: "text", text: "hi" }]);
  });

  it("decodes two events in one chunk", () => {
    const { t, events } = setup();
    t.out(`{"type":"turn_done"}\n{"type":"result","subtype":"success","result":"x"}\n`);
    expect(events.map((e) => e.type)).toEqual(["turn_done", "result"]);
  });

  it("buffers a line split across chunks", () => {
    const { t, events } = setup();
    t.out(`{"type":"te`);
    t.out(`xt","text":"split"}`);
    expect(events).toEqual([]); // no newline yet
    t.out(`\n`);
    expect(events).toEqual([{ type: "text", text: "split" }]);
  });

  it("reports a malformed line then keeps going", () => {
    const { t, events, malformed } = setup();
    t.out(`not json\n{"type":"turn_done"}\n`);
    expect(malformed).toEqual(["not json"]);
    expect(events.map((e) => e.type)).toEqual(["turn_done"]);
  });

  it("decodes new text_delta and interrupted events", () => {
    const { t, events } = setup();
    t.out(`{"type":"text_delta","text":"ab"}\n{"type":"interrupted"}\n`);
    expect(events).toEqual([{ type: "text_delta", text: "ab" }, { type: "interrupted" }]);
  });

  it("routes stderr to onStderr, not onEvent", () => {
    const { t, events, stderr } = setup();
    t.err("traceback...");
    expect(stderr).toEqual(["traceback..."]);
    expect(events).toEqual([]);
  });
});

describe("PartisanClient controls", () => {
  it("sends exact control messages", () => {
    const { t, client } = setup();
    client.user("hello");
    client.interrupt();
    client.pause();
    client.close();
    expect(t.sent).toEqual([
      { type: "user", text: "hello" },
      { type: "interrupt" },
      { type: "pause" },
      { type: "close" },
    ]);
  });

  it("abort() closes the transport", () => {
    const { t, client } = setup();
    client.abort();
    expect(t.closed).toBe(true);
  });
});

describe("PartisanClient.exportContext", () => {
  it("sends export_context and resolves on the context event (not forwarded to onEvent)", async () => {
    const { t, client, events } = setup();
    const p = client.exportContext(1000);
    expect(t.sent).toEqual([{ type: "export_context" }]);
    t.out(`${JSON.stringify({ type: "context", sessionId: "s1", transcript: [{ type: "text", text: "x" }] })}\n`);
    const ctx = await p;
    expect(ctx.sessionId).toBe("s1");
    expect(ctx.transcript).toHaveLength(1);
    expect(events).toEqual([]); // context consumed, not forwarded
  });

  it("rejects on timeout", async () => {
    const { client } = setup();
    await expect(client.exportContext(20)).rejects.toThrow(/timed out/);
  });
});

describe("PartisanClient.done", () => {
  it("exposes the transport exit code", async () => {
    const { t, client } = setup();
    t.exit(0);
    await expect(client.done).resolves.toEqual({ exitCode: 0 });
  });
});
