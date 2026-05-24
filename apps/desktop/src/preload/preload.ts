import { contextBridge, ipcRenderer, type IpcRendererEvent } from "electron";
import { IpcChannel } from "../main/ipc/channels";
import type {
  SessionSummary,
  WorkEventPush,
  WorkFilesPush,
  WorkHostPush,
  WorkStatusPush,
} from "../main/ipc/types";

function subscribe<T>(channel: string, cb: (payload: T) => void): () => void {
  const listener = (_e: IpcRendererEvent, payload: T): void => cb(payload);
  ipcRenderer.on(channel, listener);
  return () => ipcRenderer.removeListener(channel, listener);
}

// Narrow, allowlisted bridge — the only surface the sandboxed renderer can see (design.md §2).
const api = {
  getVersion: (): Promise<string> => ipcRenderer.invoke(IpcChannel.AppGetVersion),

  // WORK mode (S6.1): drive the in-guest agent over Hop 1 → Session Manager.
  work: {
    listSessions: (): Promise<SessionSummary[]> => ipcRenderer.invoke(IpcChannel.WorkListSessions),
    hostStatus: (): Promise<boolean> => ipcRenderer.invoke(IpcChannel.WorkHostStatus),
    pickFolder: (): Promise<string | null> => ipcRenderer.invoke(IpcChannel.WorkPickFolder),
    openSession: (folder: string): Promise<string> => ipcRenderer.invoke(IpcChannel.WorkOpenSession, folder),
    sendMessage: (appId: string, text: string): Promise<void> =>
      ipcRenderer.invoke(IpcChannel.WorkSendMessage, appId, text),
    resumeSession: (appId: string): Promise<void> => ipcRenderer.invoke(IpcChannel.WorkResumeSession, appId),
    stopSession: (appId: string): Promise<void> => ipcRenderer.invoke(IpcChannel.WorkStopSession, appId),
    closeSession: (appId: string): Promise<void> => ipcRenderer.invoke(IpcChannel.WorkCloseSession, appId),

    onStatus: (cb: (p: WorkStatusPush) => void): (() => void) => subscribe(IpcChannel.WorkStatus, cb),
    onEvent: (cb: (p: WorkEventPush) => void): (() => void) => subscribe(IpcChannel.WorkEvent, cb),
    onFiles: (cb: (p: WorkFilesPush) => void): (() => void) => subscribe(IpcChannel.WorkFiles, cb),
    onHost: (cb: (p: WorkHostPush) => void): (() => void) => subscribe(IpcChannel.WorkHost, cb),
  },
};

contextBridge.exposeInMainWorld("atelier", api);

export type AtelierApi = typeof api;
