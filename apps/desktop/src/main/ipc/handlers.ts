import { app, BrowserWindow, dialog, ipcMain } from "electron";
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
  const manager = new SessionManager(host, store, emit);
  void manager.init();

  ipcMain.handle(IpcChannel.WorkListSessions, () => manager.listSessions());
  ipcMain.handle(IpcChannel.WorkHostStatus, () => manager.hostStatus());
  ipcMain.handle(IpcChannel.WorkPickFolder, async () => {
    const r = await dialog.showOpenDialog({ properties: ["openDirectory"] });
    return r.canceled || r.filePaths.length === 0 ? null : r.filePaths[0];
  });
  ipcMain.handle(IpcChannel.WorkOpenSession, (_e, folder: string) => manager.openSession(folder));
  ipcMain.handle(IpcChannel.WorkSendMessage, (_e, appId: string, text: string) => manager.sendMessage(appId, text));
  ipcMain.handle(IpcChannel.WorkResumeSession, (_e, appId: string) => manager.resume(appId));
  ipcMain.handle(IpcChannel.WorkCloseSession, (_e, appId: string) => manager.closeSession(appId));

  return { shutdown: () => manager.shutdown() };
}
