import { describe, expect, it } from "vitest";
import { isWithin, Policy } from "./policy";

describe("isWithin", () => {
  it("accepts paths inside the workspace", () => {
    expect(isWithin("/workspace", "/workspace/summary.csv")).toBe(true);
    expect(isWithin("/workspace", "/workspace")).toBe(true);
    expect(isWithin("/workspace", "/workspace/sub/dir/x")).toBe(true);
  });

  it("rejects paths outside the workspace", () => {
    expect(isWithin("/workspace", "/etc/passwd")).toBe(false);
    expect(isWithin("/workspace", "/workspace-evil/x")).toBe(false);
  });

  it("defeats .. traversal", () => {
    expect(isWithin("/workspace", "/workspace/../etc/passwd")).toBe(false);
  });
});

describe("Policy", () => {
  const policy = new Policy({ workspaceRoot: "/workspace" });

  it("allows read and shell", () => {
    expect(policy.evaluate("mcp__atelier__read_file", { path: "/workspace/orders.csv" }).behavior).toBe("allow");
    expect(policy.evaluate("mcp__atelier__shell", { cmd: "python3" }).behavior).toBe("allow");
  });

  it("gates write_file to the workspace", () => {
    expect(policy.evaluate("mcp__atelier__write_file", { path: "/workspace/summary.csv" }).behavior).toBe("allow");
    expect(policy.evaluate("mcp__atelier__write_file", { path: "/etc/cron.d/x" }).behavior).toBe("deny");
  });

  it("denies unknown tools", () => {
    expect(policy.evaluate("Bash", { command: "rm -rf /" }).behavior).toBe("deny");
  });

  it("audits every decision", () => {
    const seen: string[] = [];
    const p = new Policy({ workspaceRoot: "/workspace", audit: (e) => seen.push(`${e.action}:${e.decision}`) });
    p.evaluate("mcp__atelier__write_file", { path: "/etc/x" });
    expect(seen).toEqual(["mcp__atelier__write_file:deny"]);
  });
});

describe("Policy (guest mode)", () => {
  const policy = new Policy({ workspaceRoot: "/workspace", mode: "guest" });

  it("allows the in-cage coding tools", () => {
    for (const t of ["Bash", "Read", "Write", "Edit", "MultiEdit", "Glob", "Grep", "LS", "TodoWrite"]) {
      expect(policy.evaluate(t, { file_path: "/workspace/x" }).behavior).toBe("allow");
    }
  });

  it("denies egress-bound tools", () => {
    expect(policy.evaluate("WebFetch", { url: "https://evil" }).behavior).toBe("deny");
    expect(policy.evaluate("WebSearch", { query: "x" }).behavior).toBe("deny");
  });

  it("denies unknown tools by default", () => {
    expect(policy.evaluate("SomeFutureTool", {}).behavior).toBe("deny");
  });

  it("does NOT gate writes to the workspace (the whole guest is the cage)", () => {
    expect(policy.evaluate("Write", { file_path: "/tmp/scratch" }).behavior).toBe("allow");
  });

  it("audits with door + detail", () => {
    const seen: Array<{ door: string; action: string; decision: string; path?: string }> = [];
    const p = new Policy({ workspaceRoot: "/workspace", mode: "guest", audit: (e) => seen.push(e) });
    p.evaluate("Bash", { command: "python3 totals.py" });
    p.evaluate("Write", { file_path: "/workspace/summary.csv" });
    expect(seen).toEqual([
      { door: "compute", action: "Bash", decision: "allow", reason: expect.any(String), path: "python3 totals.py" },
      { door: "files", action: "Write", decision: "allow", reason: expect.any(String), path: "/workspace/summary.csv" },
    ]);
  });
});
