import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  globalSetup: "./e2e/global-setup",
  reporter: [["list"], ["html", { outputFolder: "e2e/report", open: "never" }]],
  projects: [{ name: "electron" }],
});
