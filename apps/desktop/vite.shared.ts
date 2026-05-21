import type { Plugin } from "vite";

// @electron-forge/plugin-vite still sets the deprecated rollup output option
// `inlineDynamicImports: true` for the single-file main/preload bundles. Under
// Vite 8 (Rolldown) that option is deprecated in favour of `codeSplitting: false`,
// producing a build warning. Vite's mergeConfig can't delete a key (it skips
// undefined overrides), so rewrite it in a config hook instead. The output is
// still a single file; this only swaps the option spelling.
//
// Remove once @electron-forge/plugin-vite targets Vite 8's option names.
export function quietSingleFileBundle(): Plugin {
  return {
    name: "atelier:quiet-single-file-bundle",
    enforce: "post",
    config(config) {
      const output = config.build?.rollupOptions?.output;
      if (!output) return;
      for (const o of Array.isArray(output) ? output : [output]) {
        if (o && typeof o === "object" && "inlineDynamicImports" in o) {
          delete (o as Record<string, unknown>).inlineDynamicImports;
          (o as { codeSplitting?: boolean }).codeSplitting = false;
        }
      }
    },
  };
}
