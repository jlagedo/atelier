import { execSync } from "node:child_process";
import { join } from "node:path";

const appRoot = join(__dirname, "..");

function build(config: string) {
  execSync(`./node_modules/.bin/vite build --config ${config}`, {
    cwd: appRoot,
    stdio: "inherit",
  });
}

export default function globalSetup() {
  build("vite.main.e2e.config.ts");
  build("vite.preload.e2e.config.ts");
  build("vite.renderer.e2e.config.ts");
}
