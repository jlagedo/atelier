// Mock data for the UI shell. No backend — every value here is fabricated so the
// chat-forward layout can be designed and reviewed before the broker/agent exists.
// When M6 wires the host-client, this module is the single thing to replace.

export type Role = "user" | "assistant";
export type SessionMode = "chat" | "work";
export type SessionStatus = "idle" | "running" | "waiting" | "done" | "error";

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

export type FileKind = "csv" | "xlsx" | "md" | "py" | "txt" | "json" | "folder" | "ts";

export type WorkspaceFile = {
  name: string;
  kind: FileKind;
  size?: string;
  modified?: string;
  status?: "new" | "modified";
};

export type WorkAccess = {
  read: "allowed";
  write: "ask";
  shell: "ask";
};

export type ChangedFile = {
  path: string;
  status: "created" | "modified";
};

export type BackgroundTask = {
  id: string;
  label: string;
  status: "running" | "waiting" | "done" | "error";
};

type BaseSession = {
  id: string;
  mode: SessionMode;
  title: string;
  updatedAt: string;
  preview: string;
  status: SessionStatus;
  items: ChatItem[];
};

export type ChatSession = BaseSession & {
  mode: "chat";
  notes: string[];
  artifacts: string[];
};

export type WorkSession = BaseSession & {
  mode: "work";
  folderName: string;
  folderPath: string;
  access: WorkAccess;
  files: WorkspaceFile[];
  changedFiles: ChangedFile[];
  backgroundTasks: BackgroundTask[];
};

export type Session = ChatSession | WorkSession;

export const chatSessions: ChatSession[] = [
  {
    id: "product-launch-ideas",
    mode: "chat",
    title: "Product launch ideas",
    updatedAt: "5m",
    preview: "Explore positioning for an AI workspace",
    status: "idle",
    notes: ["Keep the tone quiet and professional", "Avoid overpromising autonomous work"],
    artifacts: ["Launch outline", "Audience notes"],
    items: [
      {
        kind: "message",
        id: "pl1",
        role: "user",
        content: "Give me product launch ideas for Atelier that do not sound like another chatbot.",
      },
      {
        kind: "message",
        id: "pl2",
        role: "assistant",
        content:
          "Lean into the workspace angle: Atelier is less about chatting with an assistant and more about having a contained place where ideas, files, and careful execution can live together.\n\nA few directions:\n\n1. A calm AI workshop for local files\n2. Agentic work without losing control\n3. Chat when you want to think, Work when you want to act",
      },
    ],
  },
  {
    id: "sandbox-architecture",
    mode: "chat",
    title: "Explain sandbox architecture",
    updatedAt: "1h",
    preview: "Describe the VM and broker boundaries",
    status: "idle",
    notes: ["Useful for docs/design.md follow-up"],
    artifacts: ["Architecture summary"],
    items: [
      {
        kind: "message",
        id: "sa1",
        role: "user",
        content: "Explain the host broker and utility VM boundary in plain language.",
      },
      {
        kind: "message",
        id: "sa2",
        role: "assistant",
        content:
          "The host broker is the controlled doorway. The utility VM is where risky work happens. Atelier routes file and command capabilities through the broker so every meaningful action can be gated and audited.",
      },
    ],
  },
  {
    id: "customer-email",
    mode: "chat",
    title: "Draft customer email",
    updatedAt: "Yesterday",
    preview: "Write a concise beta update",
    status: "idle",
    notes: [],
    artifacts: ["Beta update draft"],
    items: [
      {
        kind: "message",
        id: "ce1",
        role: "user",
        content: "Draft a short beta update email for early users.",
      },
    ],
  },
];

const atelierFiles: WorkspaceFile[] = [
  { name: "apps", kind: "folder", modified: "2m" },
  { name: "docs", kind: "folder", modified: "1h" },
  { name: "package.json", kind: "json", size: "684 B", modified: "1h" },
  { name: "App.tsx", kind: "ts", size: "1.1 KB", modified: "2m", status: "modified" },
  { name: "mock-data.ts", kind: "ts", size: "9.4 KB", modified: "just now", status: "modified" },
  { name: "design.md", kind: "md", size: "19 KB", modified: "Mon" },
];

const invoiceFiles: WorkspaceFile[] = [
  { name: "source", kind: "folder", modified: "Today" },
  { name: "invoices.csv", kind: "csv", size: "2.8 MB", modified: "Today" },
  { name: "purchase_orders.csv", kind: "csv", size: "1.7 MB", modified: "Today" },
  { name: "mismatches.md", kind: "md", size: "2 KB", modified: "just now", status: "new" },
];

const briefFiles: WorkspaceFile[] = [
  { name: "brief.md", kind: "md", size: "12 KB", modified: "Yesterday", status: "modified" },
  { name: "notes.txt", kind: "txt", size: "640 B", modified: "Yesterday" },
];

export const workSessions: WorkSession[] = [
  {
    id: "atelier-work",
    mode: "work",
    title: "atelier",
    updatedAt: "now",
    preview: "Implement Chat / Work mock shell",
    status: "running",
    folderName: "atelier",
    folderPath: "E:\\dev\\atelier",
    access: { read: "allowed", write: "ask", shell: "ask" },
    files: atelierFiles,
    changedFiles: [
      { path: "apps/desktop/src/renderer/App.tsx", status: "modified" },
      { path: "apps/desktop/src/renderer/lib/mock-data.ts", status: "modified" },
    ],
    backgroundTasks: [
      { id: "task-typecheck", label: "typecheck desktop shell", status: "running" },
      { id: "task-review", label: "waiting on write approval", status: "waiting" },
    ],
    items: [
      {
        kind: "message",
        id: "aw1",
        role: "user",
        content: "Map `E:\\dev\\atelier` and mock the new Chat / Work interface.",
      },
      {
        kind: "message",
        id: "aw2",
        role: "assistant",
        content:
          "I have the folder mapped as a Work session. I'll keep Chat and Work histories separate, then make the right panel show folder access, changed files, running tasks, and the mock file tree.",
      },
      {
        kind: "tool",
        id: "awt1",
        tool: {
          id: "awt1",
          label: "read folder",
          target: "E:\\dev\\atelier",
          status: "done",
          output: "Found desktop renderer, mock data seam, and workspace panel components.",
        },
      },
      {
        kind: "tool",
        id: "awt2",
        tool: {
          id: "awt2",
          label: "run typecheck",
          target: "apps/desktop",
          status: "running",
          output: "> tsc --noEmit\n> checking renderer session types...",
        },
      },
      {
        kind: "approval",
        id: "awa1",
        approval: {
          id: "awa1",
          action: "Write",
          target: "apps/desktop/src/renderer",
          detail: "mock Chat / Work UI changes",
          status: "pending",
        },
      },
    ],
  },
  {
    id: "invoices-may",
    mode: "work",
    title: "invoices-may",
    updatedAt: "12m",
    preview: "Match invoices against purchase orders",
    status: "waiting",
    folderName: "invoices-may",
    folderPath: "E:\\finance\\invoices-may",
    access: { read: "allowed", write: "ask", shell: "ask" },
    files: invoiceFiles,
    changedFiles: [{ path: "mismatches.md", status: "created" }],
    backgroundTasks: [{ id: "task-invoice-approval", label: "approval to write mismatch report", status: "waiting" }],
    items: [
      {
        kind: "message",
        id: "im1",
        role: "user",
        content: "Find invoices that do not have matching purchase orders.",
      },
      {
        kind: "message",
        id: "im2",
        role: "assistant",
        content: "I found 18 likely mismatches and am waiting before writing the report.",
      },
    ],
  },
  {
    id: "client-brief",
    mode: "work",
    title: "client-brief",
    updatedAt: "Yesterday",
    preview: "Clean up a client brief draft",
    status: "done",
    folderName: "client-brief",
    folderPath: "E:\\clients\\brief",
    access: { read: "allowed", write: "ask", shell: "ask" },
    files: briefFiles,
    changedFiles: [{ path: "brief.md", status: "modified" }],
    backgroundTasks: [{ id: "task-brief", label: "rewrite brief sections", status: "done" }],
    items: [
      {
        kind: "message",
        id: "cb1",
        role: "user",
        content: "Tighten the client brief and remove repeated sections.",
      },
      {
        kind: "message",
        id: "cb2",
        role: "assistant",
        content: "Done. I tightened the overview and removed duplicate delivery notes.",
      },
    ],
  },
];

export const sessions: Session[] = [...chatSessions, ...workSessions];

export const defaultSessionIds: Record<SessionMode, string> = {
  chat: "product-launch-ideas",
  work: "atelier-work",
};

export function sessionsForMode(mode: SessionMode): Session[] {
  return sessions.filter((session) => session.mode === mode);
}
