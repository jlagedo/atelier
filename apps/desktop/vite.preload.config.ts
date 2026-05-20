import { defineConfig } from "vite";

// Preload script (Node bridge). Electron is provided by the runtime, never bundled.
export default defineConfig({
  build: {
    rollupOptions: { external: ["electron"] },
  },
});
