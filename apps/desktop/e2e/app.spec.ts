import { test } from "@playwright/test";
import { _electron as electron } from "playwright";
import { join } from "node:path";

const appRoot = join(__dirname, "..");

test("initial screen", async () => {
  const app = await electron.launch({
    args: [join(appRoot, ".vite/build/main.js")],
    cwd: appRoot,
  });

  const window = await app.firstWindow();
  await window.waitForLoadState("load");
  // Wait for React to mount — the sidebar wordmark is the first stable landmark.
  await window.waitForSelector("text=Atelier", { timeout: 10000 });
  // One extra frame for fonts + CSS animations to settle.
  await window.waitForTimeout(300);

  await window.screenshot({
    path: join(appRoot, "e2e/screenshots/initial-screen.png"),
  });

  await app.close();
});
