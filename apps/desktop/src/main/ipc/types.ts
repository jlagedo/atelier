// Wire payload types for the WORK-mode IPC seam (Hop 1). Type-only — safe to import
// from both main (handlers) and the preload bridge without pulling runtime deps.

import type { LoopEvent } from "../host-client/types";
import type { SessionLifecycle, SessionSummary } from "../sessions/manager";
import type { WorkspaceUpdate } from "../workspace/watcher";

export type { LoopEvent, SessionLifecycle, SessionSummary, WorkspaceUpdate };

export interface WorkStatusPush {
  appId: string;
  status: SessionLifecycle;
  meta?: Record<string, unknown>;
}

export interface WorkEventPush {
  appId: string;
  event: LoopEvent;
}

export interface WorkFilesPush {
  appId: string;
  update: WorkspaceUpdate;
}

export interface WorkHostPush {
  up: boolean;
}
