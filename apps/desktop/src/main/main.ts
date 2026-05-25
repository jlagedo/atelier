import { app, BrowserWindow } from "electron";
import path from "node:path";
import { existsSync } from "node:fs";
import { installCsp } from "./security";
import { registerIpcHandlers, type IpcBackend } from "./ipc/handlers";

let backend: IpcBackend | undefined;

function installDevDockIcon(): void {
  if (process.platform !== "darwin" || app.isPackaged) return;

  // Cosmetic dev-only nicety: never let a missing/unloadable icon abort startup
  // (the asset isn't bundled in the e2e build, and setIcon throws on a bad path).
  const icon = path.join(app.getAppPath(), "assets", "icon.png");
  if (!existsSync(icon)) return;
  try {
    app.dock?.setIcon(icon);
  } catch {
    /* ignore — the dock icon is not load-bearing */
  }
}

function createWindow(): void {
  const win = new BrowserWindow({
    width: 1200,
    height: 800,
    title: "Atelier",
    // Pre-paint flash colour for the native window; can't read a CSS var, so it
    // mirrors --background of the default (.dark / "Aegean Dusk") theme.
    backgroundColor: "#0E191F",
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  if (MAIN_WINDOW_VITE_DEV_SERVER_URL) {
    void win.loadURL(MAIN_WINDOW_VITE_DEV_SERVER_URL);
  } else {
    void win.loadFile(path.join(__dirname, `../renderer/${MAIN_WINDOW_VITE_NAME}/index.html`));
  }
}

void app.whenReady().then(() => {
  installCsp();
  installDevDockIcon();
  backend = registerIpcHandlers();
  createWindow();

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

// Best-effort teardown: stop live loops + the shared VM (design.md §8). Electron
// won't await async quit handlers, so this is fire-and-forget.
app.on("will-quit", () => {
  void backend?.shutdown();
});
