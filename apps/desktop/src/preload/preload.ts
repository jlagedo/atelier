import { contextBridge, ipcRenderer } from "electron";
import { IpcChannel } from "../main/ipc/channels";

// Narrow, allowlisted bridge — the only surface the sandboxed renderer can see (design.md §2).
const api = {
  getVersion: (): Promise<string> => ipcRenderer.invoke(IpcChannel.AppGetVersion),
};

contextBridge.exposeInMainWorld("atelier", api);

export type AtelierApi = typeof api;
