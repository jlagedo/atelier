// Mock data for the UI shell. No backend — every value here is fabricated so the
// chat-forward layout can be designed and reviewed before the broker/agent exists.
// When M6 wires the host-client, this module is the single thing to replace.

export type Role = "user" | "assistant";

export type ToolCall = {
  id: string;
  label: string;
  target: string;
  status: "done" | "running" | "error";
  output: string;
};

export type Approval = {
  id: string;
  action: string;
  target: string;
  detail: string;
  status: "pending" | "approved" | "denied";
};

export type ChatItem =
  | { kind: "message"; id: string; role: Role; content: string }
  | { kind: "tool"; id: string; tool: ToolCall }
  | { kind: "approval"; id: string; approval: Approval };

export type Conversation = {
  id: string;
  title: string;
  updatedAt: string;
  preview: string;
  items: ChatItem[];
};

export type FileKind = "csv" | "xlsx" | "md" | "py" | "txt" | "json" | "folder";

export type WorkspaceFile = {
  name: string;
  kind: FileKind;
  size?: string;
  modified?: string;
  status?: "new" | "modified";
};

export const conversations: Conversation[] = [
  {
    id: "orders-recon",
    title: "Orders reconciliation",
    updatedAt: "2m",
    preview: "Flag duplicate order IDs in orders.csv",
    items: [
      {
        kind: "message",
        id: "m1",
        role: "user",
        content: "Summarise `orders.csv` in my workspace and flag any duplicate order IDs.",
      },
      {
        kind: "message",
        id: "m2",
        role: "assistant",
        content:
          "I'll read the file, scan for duplicate order IDs in Python, and write a short summary back to your workspace.\n\nHere's the plan:\n\n1. Read `/workspace/orders.csv`\n2. Group by `order_id` and count occurrences\n3. Write `summary.md` with the findings",
      },
      {
        kind: "tool",
        id: "t1",
        tool: {
          id: "t1",
          label: "read file",
          target: "/workspace/orders.csv",
          status: "done",
          output: "12,480 rows · 9 columns · 1.4 MB",
        },
      },
      {
        kind: "tool",
        id: "t2",
        tool: {
          id: "t2",
          label: "ran python",
          target: "scan_duplicates.py",
          status: "done",
          output:
            "> grouping by order_id\n> 3 duplicate order IDs found: BNY-10455, BNY-10812, BNY-11067\n> 6 affected rows",
        },
      },
      {
        kind: "message",
        id: "m3",
        role: "assistant",
        content:
          "Done. Quick summary:\n\n| Metric | Value |\n| --- | --- |\n| Total orders | 12,480 |\n| Unique order IDs | 12,477 |\n| **Duplicates** | **3** (6 rows) |\n\nThe duplicates are `BNY-10455`, `BNY-10812`, and `BNY-11067`. I'd like to write the full breakdown to `summary.md`.",
      },
      {
        kind: "approval",
        id: "a1",
        approval: {
          id: "a1",
          action: "Write",
          target: "summary.md",
          detail: "to /workspace",
          status: "pending",
        },
      },
    ],
  },
  {
    id: "q2-variance",
    title: "Q2 variance analysis",
    updatedAt: "1h",
    preview: "Compare actuals vs budget by cost centre",
    items: [
      {
        kind: "message",
        id: "qm1",
        role: "user",
        content: "Compare actuals vs budget by cost centre in `q2_variance.xlsx`.",
      },
      {
        kind: "message",
        id: "qm2",
        role: "assistant",
        content: "On it — I'll load the workbook and compute the variance per cost centre.",
      },
    ],
  },
  {
    id: "payroll-cleanup",
    title: "Payroll export cleanup",
    updatedAt: "Yesterday",
    preview: "Normalise employee IDs and trailing whitespace",
    items: [
      {
        kind: "message",
        id: "pm1",
        role: "user",
        content: "Clean up the payroll export — normalise employee IDs and strip whitespace.",
      },
    ],
  },
  {
    id: "vendor-invoice",
    title: "Vendor invoice match",
    updatedAt: "Yesterday",
    preview: "Match invoices.csv against purchase_orders.csv",
    items: [
      {
        kind: "message",
        id: "vm1",
        role: "user",
        content: "Match `invoices.csv` against `purchase_orders.csv` and list mismatches.",
      },
    ],
  },
  {
    id: "fx-refresh",
    title: "FX rates refresh",
    updatedAt: "Mon",
    preview: "Pull latest EUR/USD and rebuild the rates sheet",
    items: [
      {
        kind: "message",
        id: "fm1",
        role: "user",
        content: "Refresh the FX rates sheet with the latest EUR/USD close.",
      },
    ],
  },
];

export const activeConversationId = "orders-recon";

export const workspaceFiles: WorkspaceFile[] = [
  { name: "archive", kind: "folder", modified: "Apr 30" },
  { name: "orders.csv", kind: "csv", size: "1.4 MB", modified: "2m", status: "modified" },
  { name: "summary.md", kind: "md", size: "412 B", modified: "just now", status: "new" },
  { name: "scan_duplicates.py", kind: "py", size: "1.1 KB", modified: "3m" },
  { name: "q2_variance.xlsx", kind: "xlsx", size: "84 KB", modified: "1h" },
  { name: "vendors.csv", kind: "csv", size: "22 KB", modified: "Yesterday" },
  { name: "config.json", kind: "json", size: "2 KB", modified: "Mon" },
  { name: "notes.txt", kind: "txt", size: "640 B", modified: "Mon" },
];

export const workspaceRoot = "~/Atelier/workspace";
