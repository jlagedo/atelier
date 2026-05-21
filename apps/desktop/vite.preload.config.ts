import { defineConfig } from "vite";
import { quietSingleFileBundle } from "./vite.shared";

// Preload script (Node bridge). Electron is provided by the runtime, never bundled.
export default defineConfig({
  build: {
    rollupOptions: { external: ["electron"] },
  },
  plugins: [quietSingleFileBundle()],
});
