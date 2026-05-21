import { defineConfig } from "vite";
import { quietSingleFileBundle } from "./vite.shared";

// Main process (Node). Electron is provided by the runtime, never bundled.
export default defineConfig({
  build: {
    rollupOptions: { external: ["electron"] },
  },
  resolve: {
    mainFields: ["module", "jsnext:main", "jsnext"],
  },
  plugins: [quietSingleFileBundle()],
});
