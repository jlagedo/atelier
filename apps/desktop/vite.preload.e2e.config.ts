import { defineConfig } from "vite";
import { builtinModules } from "node:module";
import { quietSingleFileBundle } from "./vite.shared";

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
      input: "src/preload/preload.ts",
      output: {
        format: "cjs",
        entryFileNames: "preload.js",
      },
    },
  },
});
