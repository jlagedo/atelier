import { app, ipcMain } from "electron";
import { IpcChannel } from "./channels";

// Hop 1 (design.md §8): typed ipcMain handlers, the renderer's only path into main.
export function registerIpcHandlers(): void {
  ipcMain.handle(IpcChannel.AppGetVersion, () => app.getVersion());
}
