// Live WORK-mode store (S6.1): subscribes to the main process's work pushes and
// maps the in-guest loop's NDJSON events into the renderer's WorkSession shape.
// Keyed by appId so N sessions stay independent; only a bounded number are live
// (the rest are dormant, rebuilt from the durable store on launch).

import { useCallback, useEffect, useRef, useState } from "react";
import {
  CAGE_ACCESS,
  type BackgroundTask,
  type ChatItem,
  type SessionStatus,
  type WorkSession,
} from "@/lib/mock-data";
import type { LoopEvent, SessionSummary, WorkEventPush, WorkFilesPush, WorkStatusPush } from "../../main/ipc/types";

const rid = (): string => globalThis.crypto.randomUUID();

function basename(p: string): string {
  const parts = p.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? p;
}

function toolTarget(input: unknown): string {
  if (input && typeof input === "object") {
    const o = input as Record<string, unknown>;
    for (const k of ["file_path", "path", "command", "pattern", "notebook_path"]) {
      const v = o[k];
      if (typeof v === "string" && v) return v;
    }
  }
  return "";
}

// Fold one loop event into the rendered item list (used live and for rebuild).
function reduceItems(items: ChatItem[], ev: LoopEvent): ChatItem[] {
  switch (ev.type) {
    case "text": {
      const last = items[items.length - 1];
      if (last && last.kind === "message" && last.role === "assistant") {
        return [...items.slice(0, -1), { ...last, content: last.content + ev.text }];
      }
      return [...items, { kind: "message", id: rid(), role: "assistant", content: ev.text }];
    }
    case "tool_use":
      return [
        ...items,
        { kind: "tool", id: ev.id, tool: { id: ev.id, label: ev.name, target: toolTarget(ev.input), status: "running", output: "" } },
      ];
    case "tool_result":
      return items.map((it) =>
        it.kind === "tool" && it.tool.id === ev.id
          ? { ...it, tool: { ...it.tool, status: ev.isError ? "error" : "done", output: ev.content } }
          : it,
      );
    case "policy":
      return [
        ...items,
        { kind: "policy", id: rid(), policy: { id: rid(), action: ev.action, target: ev.detail, decision: ev.decision, reason: ev.reason } },
      ];
    default:
      return items; // init/context/result/turn_done/error carry no chat item
  }
}

function deriveTasks(status: SessionStatus): BackgroundTask[] {
  if (status === "running") return [{ id: "agent", label: "agent working", status: "running" }];
  if (status === "starting" || status === "resuming") return [{ id: "agent", label: "bringing up session", status: "running" }];
  return [];
}

function baseSession(appId: string, title: string, folderPath: string): WorkSession {
  return {
    id: appId,
    mode: "work",
    title,
    updatedAt: "now",
    preview: "",
    status: "starting",
    items: [],
    folderName: basename(folderPath),
    folderPath,
    access: CAGE_ACCESS,
    files: [],
    changedFiles: [],
    backgroundTasks: [],
  };
}

function withStatus(s: WorkSession, status: SessionStatus): WorkSession {
  return { ...s, status, backgroundTasks: deriveTasks(status) };
}

function summaryToSession(s: SessionSummary): WorkSession {
  const sess = baseSession(s.appId, s.title, s.folder);
  sess.status = s.status;
  sess.items = (Array.isArray(s.transcript) ? (s.transcript as LoopEvent[]) : []).reduce(reduceItems, [] as ChatItem[]);
  return sess;
}

export interface WorkSessionsApi {
  list: WorkSession[];
  active: WorkSession | null;
  activeId: string | null;
  hostUp: boolean | null;
  newWork: () => Promise<void>;
  send: (text: string) => void;
  select: (id: string) => void;
  close: (id: string) => void;
}

export function useWorkSessions(): WorkSessionsApi {
  const [sessions, setSessions] = useState<Map<string, WorkSession>>(new Map());
  const [activeId, setActiveId] = useState<string | null>(null);
  const [hostUp, setHostUp] = useState<boolean | null>(null);
  const activeRef = useRef<string | null>(null);
  activeRef.current = activeId;

  const update = useCallback((appId: string, fn: (s: WorkSession) => WorkSession): void => {
    setSessions((prev) => {
      const cur = prev.get(appId);
      if (!cur) return prev;
      const next = new Map(prev);
      next.set(appId, fn(cur));
      return next;
    });
  }, []);

  useEffect(() => {
    const api = window.atelier?.work;
    if (!api) {
      setHostUp(false);
      return;
    }
    let live = true;

    api.hostStatus().then((up) => live && setHostUp(up)).catch(() => live && setHostUp(false));
    api
      .listSessions()
      .then((listed) => {
        if (!live) return;
        setSessions((prev) => {
          const next = new Map(prev);
          for (const s of listed) if (!next.has(s.appId)) next.set(s.appId, summaryToSession(s));
          return next;
        });
        if (!activeRef.current && listed.length > 0) setActiveId(listed[0].appId);
      })
      .catch(() => undefined);

    const offHost = api.onHost((p) => setHostUp(p.up));
    const offStatus = api.onStatus((p: WorkStatusPush) => {
      setSessions((prev) => {
        const next = new Map(prev);
        const cur = next.get(p.appId);
        const title = (p.meta?.title as string | undefined) ?? cur?.title ?? p.appId;
        const folder = (p.meta?.folder as string | undefined) ?? cur?.folderPath ?? "";
        const seed = cur ?? baseSession(p.appId, title, folder);
        next.set(p.appId, withStatus({ ...seed, title, folderPath: folder, folderName: basename(folder) }, p.status));
        return next;
      });
    });
    const offEvent = api.onEvent((p: WorkEventPush) => {
      update(p.appId, (s) => {
        const items = reduceItems(s.items, p.event);
        if (p.event.type === "turn_done") return withStatus({ ...s, items }, "active");
        if (p.event.type === "error") return withStatus({ ...s, items }, "error");
        return { ...s, items };
      });
    });
    const offFiles = api.onFiles((p: WorkFilesPush) => {
      update(p.appId, (s) => ({ ...s, files: p.update.files, changedFiles: p.update.changedFiles }));
    });

    return () => {
      live = false;
      offHost();
      offStatus();
      offEvent();
      offFiles();
    };
  }, [update]);

  const newWork = useCallback(async (): Promise<void> => {
    const api = window.atelier?.work;
    if (!api) return;
    const folder = await api.pickFolder();
    if (!folder) return;
    const appId = await api.openSession(folder);
    setActiveId(appId);
  }, []);

  const send = useCallback(
    (text: string): void => {
      const id = activeRef.current;
      const api = window.atelier?.work;
      if (!id || !api) return;
      update(id, (s) =>
        withStatus({ ...s, items: [...s.items, { kind: "message", id: rid(), role: "user", content: text }] }, "running"),
      );
      void api.sendMessage(id, text).catch(() => undefined);
    },
    [update],
  );

  const select = useCallback((id: string): void => {
    setActiveId(id);
    const api = window.atelier?.work;
    setSessions((prev) => {
      const s = prev.get(id);
      if (s && s.status === "inactive" && api) void api.resumeSession(id).catch(() => undefined);
      return prev;
    });
  }, []);

  const close = useCallback((id: string): void => {
    void window.atelier?.work?.closeSession(id).catch(() => undefined);
    setSessions((prev) => {
      const next = new Map(prev);
      next.delete(id);
      if (activeRef.current === id) setActiveId(next.size > 0 ? [...next.keys()][0] : null);
      return next;
    });
  }, []);

  return {
    list: [...sessions.values()],
    active: activeId ? (sessions.get(activeId) ?? null) : null,
    activeId,
    hostUp,
    newWork,
    send,
    select,
    close,
  };
}
