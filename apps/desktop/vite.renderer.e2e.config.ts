import { defineConfig, mergeConfig } from "vite";
import baseConfig from "./vite.renderer.config";

// Output to the path main.ts resolves at runtime:
// path.join(__dirname, `../renderer/${MAIN_WINDOW_VITE_NAME}/index.html`)
// => .vite/build/../renderer/main_window/index.html
export default mergeConfig(baseConfig, defineConfig({
  // base must be relative so asset paths work under file:// (Electron loadFile).
  base: "./",
  build: {
    outDir: ".vite/renderer/main_window",
  },
}));
