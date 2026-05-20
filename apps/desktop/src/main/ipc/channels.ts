// Single source of truth for IPC channel names, shared by main (handlers) and preload (bridge).
export const IpcChannel = {
  AppGetVersion: "app:getVersion",
} as const;

export type IpcChannelName = (typeof IpcChannel)[keyof typeof IpcChannel];
