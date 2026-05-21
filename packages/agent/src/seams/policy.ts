// approvals seam (design.md §8, §10): in an enterprise deployment the end user has
// no say over approvals — policy is pre-baked by the operator and enforced + audited
// automatically (no interactive prompt). This evaluates each tool call against that
// pre-baked policy; the SDK's canUseTool callback turns the result into allow/deny.
// The broker's server-side gate remains the enforcement/audit point on the wire
// (AllowAll today); when it grows real Ask/Deny logic this policy moves there
// (a checkPolicy RPC) so the host holds authority. For S5a.1 it lives here.

export type Behavior = "allow" | "deny";

export interface Decision {
  behavior: Behavior;
  reason: string;
}

export type Door = "files" | "compute" | "other";

export interface AuditEntry {
  door: Door;
  action: string;
  decision: Behavior;
  reason: string;
  path?: string;
}

export interface PolicyConfig {
  /** The workspace root the agent may write under (default /workspace). */
  workspaceRoot: string;
  /** Audit sink for every decision — the audit record is the "approval". */
  audit?: (entry: AuditEntry) => void;
}

const TOOL = {
  shell: "mcp__atelier__shell",
  readFile: "mcp__atelier__read_file",
  writeFile: "mcp__atelier__write_file",
} as const;

/** Collapse "." and ".." segments into an absolute POSIX path. */
export function normalizePosix(p: string): string {
  const parts: string[] = [];
  for (const seg of p.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") parts.pop();
    else parts.push(seg);
  }
  return "/" + parts.join("/");
}

/** Resolve a guest path the model supplied: absolute paths stay, relative paths
 *  are taken as relative to the workspace root. The result is normalized. */
export function resolveGuestPath(root: string, p: string): string {
  const abs = p.startsWith("/") ? p : `${root.replace(/\/+$/, "")}/${p}`;
  return normalizePosix(abs);
}

/** True if p is root itself or lies beneath it (after normalization). */
export function isWithin(root: string, p: string): boolean {
  const r = normalizePosix(root);
  const t = normalizePosix(p);
  return t === r || t.startsWith(r === "/" ? "/" : r + "/");
}

export class Policy {
  constructor(private readonly cfg: PolicyConfig) {}

  evaluate(toolName: string, input: Record<string, unknown>): Decision {
    const d = this.decide(toolName, input);
    this.cfg.audit?.({
      door: doorFor(toolName),
      action: toolName,
      decision: d.behavior,
      reason: d.reason,
      path: typeof input?.path === "string" ? input.path : undefined,
    });
    return d;
  }

  private decide(toolName: string, input: Record<string, unknown>): Decision {
    switch (toolName) {
      case TOOL.readFile:
      case TOOL.shell:
        return { behavior: "allow", reason: "read-only/compute permitted by policy" };
      case TOOL.writeFile: {
        const p = String(input?.path ?? "");
        if (p && isWithin(this.cfg.workspaceRoot, resolveGuestPath(this.cfg.workspaceRoot, p))) {
          return { behavior: "allow", reason: `write within workspace ${this.cfg.workspaceRoot}` };
        }
        return { behavior: "deny", reason: `write to "${p}" is outside workspace ${this.cfg.workspaceRoot}` };
      }
      default:
        return { behavior: "deny", reason: `tool ${toolName} is not permitted` };
    }
  }
}

function doorFor(toolName: string): Door {
  if (toolName === TOOL.shell) return "compute";
  if (toolName === TOOL.readFile || toolName === TOOL.writeFile) return "files";
  return "other";
}
