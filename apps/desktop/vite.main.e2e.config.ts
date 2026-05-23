import { defineConfig } from "vite";
import { builtinModules } from "node:module";
import { quietSingleFileBundle } from "./vite.shared";

// Replicates what @electron-forge/plugin-vite injects for production main-process builds.
// Used by the E2E global setup so Playwright can launch the real Electron binary.
const nodeExternals = [
  ...builtinModules,
  ...builtinModules.map((m) => `node:${m}`),
];

export default defineConfig({
  plugins: [quietSingleFileBundle()],
  build: {
    outDir: ".vite/build",
    emptyOutDir: false,
    rollupOptions: {
      external: ["electron", ...nodeExternals],
      input: "src/main/main.ts",
      output: {
        format: "cjs",
        entryFileNames: "main.js",
      },
    },
  },
  define: {
    MAIN_WINDOW_VITE_NAME: JSON.stringify("main_window"),
    MAIN_WINDOW_VITE_DEV_SERVER_URL: '""',
  },
});
