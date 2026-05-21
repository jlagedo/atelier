// approvals seam (design.md §8, §10): in an enterprise deployment the end user has
// no say over approvals — policy is pre-baked by the operator and enforced + audited
// automatically (no interactive prompt). This evaluates each tool call against that
// pre-baked policy; the SDK's canUseTool callback turns the result into allow/deny.
// The broker's server-side gate remains the enforcement/audit point on the wire
// (AllowAll today); when it grows real Ask/Deny logic this policy moves there
// (a checkPolicy RPC) so the host holds authority.
//
// Two topologies, one policy:
//   - HOST  (S5a.1): the agent's hands are the atelier MCP tools (mcp__atelier__*)
//     that route over the broker; only those three verbs exist.
//   - GUEST (S5b.1): the loop runs INSIDE the cage, so it uses the SDK's BUILT-IN
//     coding tools (Bash/Read/Write/Edit/Glob/Grep/…) acting on the guest fs. Those
//     are safe because the whole guest is the sandbox; we allow the coding set and
//     deny anything that would reach OUT of the cage (WebFetch/WebSearch) or is
//     unknown. Every decision is still audited.

export type Behavior = "allow" | "deny";

export type Mode = "host" | "guest";

export interface Decision {
  behavior: Behavior;
  reason: string;
}

export type Door = "files" | "compute" | "network" | "other";

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
  /** Which topology this loop runs in. Default "host" (Topology A). */
  mode?: Mode;
  /** Audit sink for every decision — the audit record is the "approval". */
  audit?: (entry: AuditEntry) => void;
}

// Topology A — the atelier MCP tools.
const TOOL = {
  shell: "mcp__atelier__shell",
  readFile: "mcp__atelier__read_file",
  writeFile: "mcp__atelier__write_file",
} as const;

// Topology B — SDK built-in tools that are safe INSIDE the cage (guest fs is the
// sandbox). Includes background-shell management and the file/search verbs.
const GUEST_ALLOW = new Set<string>([
  "Bash",
  "BashOutput",
  "KillShell",
  "KillBash",
  "Read",
  "Write",
  "Edit",
  "MultiEdit",
  "NotebookEdit",
  "Glob",
  "Grep",
  "LS",
  "TodoWrite",
  "ExitPlanMode",
]);

// Tools that would punch OUT of the cage (need network egress beyond the model
// host) — denied by policy. Anything not listed anywhere is denied by default.
const GUEST_DENY = new Set<string>(["WebFetch", "WebSearch"]);

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
  private readonly mode: Mode;

  constructor(private readonly cfg: PolicyConfig) {
    this.mode = cfg.mode ?? "host";
  }

  evaluate(toolName: string, input: Record<string, unknown>): Decision {
    const d = this.mode === "guest" ? this.decideGuest(toolName) : this.decideHost(toolName, input);
    this.cfg.audit?.({
      door: doorFor(toolName),
      action: toolName,
      decision: d.behavior,
      reason: d.reason,
      path: detailOf(input),
    });
    return d;
  }

  private decideHost(toolName: string, input: Record<string, unknown>): Decision {
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

  // Inside the cage every write lands in the sandbox, so the coding tools are
  // permitted (and audited); only the whole guest is the boundary. Egress-bound
  // tools and unknown tools are denied.
  private decideGuest(toolName: string): Decision {
    if (GUEST_ALLOW.has(toolName)) {
      return { behavior: "allow", reason: "in-cage coding tool permitted by policy" };
    }
    if (GUEST_DENY.has(toolName)) {
      return { behavior: "deny", reason: `${toolName} would reach outside the cage` };
    }
    return { behavior: "deny", reason: `tool ${toolName} is not permitted` };
  }
}

/** The most useful single detail to log for a tool call (path/command/pattern). */
function detailOf(input: Record<string, unknown>): string | undefined {
  for (const k of ["path", "file_path", "notebook_path", "command", "pattern"]) {
    const v = input?.[k];
    if (typeof v === "string" && v.length > 0) return v;
  }
  return undefined;
}

function doorFor(toolName: string): Door {
  // Host (atelier MCP) tools.
  if (toolName === TOOL.shell) return "compute";
  if (toolName === TOOL.readFile || toolName === TOOL.writeFile) return "files";
  // Guest (built-in) tools.
  if (toolName.startsWith("Bash") || toolName === "KillShell" || toolName === "KillBash") return "compute";
  if (toolName === "WebFetch" || toolName === "WebSearch") return "network";
  if (["Read", "Write", "Edit", "MultiEdit", "NotebookEdit", "Glob", "Grep", "LS"].includes(toolName)) {
    return "files";
  }
  return "other";
}
