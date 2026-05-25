import { test, type Page } from "@playwright/test";
import { _electron as electron } from "playwright";
import { join } from "node:path";

// Captures both themes (dark "Aegean Dusk" + light "Studio") and a Work-mode view
// for visual review of the color system — see docs/color-system.md.
const appRoot = join(__dirname, "..");
const shots = join(appRoot, "e2e/screenshots");

// The toggle button's label depends on the current theme (which persists in
// localStorage between runs), so set the theme by checking the <html> class.
async function setTheme(window: Page, theme: "dark" | "light"): Promise<void> {
  const isDark = await window.evaluate(() => document.documentElement.classList.contains("dark"));
  if ((theme === "dark") !== isDark) {
    await window.getByRole("button", { name: /Switch to (light|dark) theme/ }).click();
    await window.waitForTimeout(400);
  }
}

test("theme screenshots (dark + light)", async () => {
  test.setTimeout(60000);

  const app = await electron.launch({ args: [join(appRoot, ".vite/build/main.js")], cwd: appRoot });
  const window = await app.firstWindow();
  await window.waitForLoadState("load");
  await window.waitForSelector("text=Atelier", { timeout: 10000 });

  await setTheme(window, "dark");
  await window.waitForTimeout(300);
  await window.screenshot({ path: join(shots, "theme-dark.png") });

  await setTheme(window, "light");
  await window.screenshot({ path: join(shots, "theme-light.png") });

  await app.close();
});

test("work mode (empty state)", async () => {
  test.setTimeout(60000);

  const app = await electron.launch({ args: [join(appRoot, ".vite/build/main.js")], cwd: appRoot });
  const window = await app.firstWindow();
  await window.waitForLoadState("load");
  await window.waitForSelector("text=Atelier", { timeout: 10000 });

  await setTheme(window, "dark");
  await window.getByRole("button", { name: "Work", exact: true }).click();
  await window.waitForTimeout(800);

  // Empty state only — driving a live session (vm0 boot) is deferred.
  await window.screenshot({ path: join(shots, "theme-work-dark.png") });
  await app.close();
});
