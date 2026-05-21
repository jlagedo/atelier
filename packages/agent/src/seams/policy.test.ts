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
