// Single source of truth for IPC channel names, shared by main (handlers) and preload (bridge).
export const IpcChannel = {
  AppGetVersion: "app:getVersion",

  // WORK mode (S6.1) — invoke (renderer → main, request/response).
  WorkListSessions: "work:listSessions",
  WorkHostStatus: "work:hostStatus",
  WorkPickFolder: "work:pickFolder",
  WorkOpenSession: "work:openSession",
  WorkSendMessage: "work:sendMessage",
  WorkResumeSession: "work:resumeSession",
  WorkCloseSession: "work:closeSession",

  // WORK mode — push (main → renderer, webContents.send). Each carries a sessionId.
  WorkStatus: "work:status",
  WorkEvent: "work:event",
  WorkFiles: "work:files",
  WorkHost: "work:host",
} as const;

export type IpcChannelName = (typeof IpcChannel)[keyof typeof IpcChannel];
