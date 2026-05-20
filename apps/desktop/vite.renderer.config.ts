import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Renderer (browser). React + Tailwind v4 (the Vite plugin replaces the old PostCSS setup).
export default defineConfig({
  plugins: [react(), tailwindcss()],
});
