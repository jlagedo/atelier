import { app, BrowserWindow, dialog, ipcMain, type IpcMainInvokeEvent } from "electron";
import path from "node:path";
import { IpcChannel } from "./channels";
import { HostClient } from "../host-client";
import { SessionStore } from "../sessions/store";
import { SessionManager, type ManagerEmitter } from "../sessions/manager";

/** Handle to tear the WORK backend down on quit. */
export interface IpcBackend {
  shutdown(): Promise<void>;
}

function broadcast(channel: string, payload: unknown): void {
  for (const w of BrowserWindow.getAllWindows()) {
    if (!w.isDestroyed()) w.webContents.send(channel, payload);
  }
}

// Like ipcMain.handle, but logs a rejection in main before re-throwing it across the
// bridge — otherwise a failed WORK action is invisible (the renderer can't see main's
// stack, and our renderer callers swallow the rejection).
function handle(channel: string, listener: (e: IpcMainInvokeEvent, ...args: any[]) => unknown): void {
  ipcMain.handle(channel, async (e, ...args) => {
    try {
      return await listener(e, ...args);
    } catch (err) {
      console.error(`[ipc ${channel}]`, err);
      throw err;
    }
  });
}

// Hop 1 (design.md §8): typed ipcMain handlers, the renderer's only path into main.
// Wires WORK mode to the Session Manager (Hop 2 → broker → guest) and fans the
// manager's status/event/files/host pushes out to every renderer window.
export function registerIpcHandlers(): IpcBackend {
  ipcMain.handle(IpcChannel.AppGetVersion, () => app.getVersion());

  const host = new HostClient();
  const store = new SessionStore(app.getPath("userData"));
  const emit: ManagerEmitter = {
    status: (appId, status, meta) => broadcast(IpcChannel.WorkStatus, { appId, status, meta }),
    event: (appId, event) => broadcast(IpcChannel.WorkEvent, { appId, event }),
    files: (appId, update) => broadcast(IpcChannel.WorkFiles, { appId, update }),
    host: (up) => broadcast(IpcChannel.WorkHost, { up }),
  };
  // Where the per-target VM bundle lives: packaged app keeps it under Resources (wired by
  // S10 packaging); in dev it's the orchestrator's output (build/debug/image), with app root =
  // apps/desktop. ATELIER_BUNDLE_DIR overrides this (e.g. to point at build/release/image).
  const bundleBaseDir = app.isPackaged
    ? path.join(process.resourcesPath, "bundle")
    : path.join(app.getAppPath(), "..", "..", "build", "debug", "image");
  const manager = new SessionManager(host, store, emit, { bundleBaseDir });
  void manager.init();

  handle(IpcChannel.WorkListSessions, () => manager.listSessions());
  handle(IpcChannel.WorkHostStatus, () => manager.hostStatus());
  handle(IpcChannel.WorkPickFolder, async () => {
    const r = await dialog.showOpenDialog({ properties: ["openDirectory"] });
    return r.canceled || r.filePaths.length === 0 ? null : r.filePaths[0];
  });
  handle(IpcChannel.WorkOpenSession, (_e, folder: string) => manager.openSession(folder));
  handle(IpcChannel.WorkSendMessage, (_e, appId: string, text: string) => manager.sendMessage(appId, text));
  handle(IpcChannel.WorkResumeSession, (_e, appId: string) => manager.resume(appId));
  handle(IpcChannel.WorkStopSession, (_e, appId: string) => manager.stopSession(appId));
  handle(IpcChannel.WorkCloseSession, (_e, appId: string) => manager.closeSession(appId));

  return { shutdown: () => manager.shutdown() };
}
