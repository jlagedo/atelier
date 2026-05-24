// Platform-aware VM bundle resolution. The Go broker takes whatever paths we hand it;
// which bundle (per-target subdir) and which rootfs disk format depend on the host OS:
// Windows boots a VHD on HCS, macOS attaches a raw ext4 image on Virtualization.framework
// (design.md §7). Kept pure (no electron) so it's unit-testable; the electron-specific base
// dir is injected by the composition root (ipc/handlers.ts).

import path from "node:path";

// TARGET names from image/build.sh — must match the per-target subdir under the bundle base
// (the orchestrator's build/<config>/image/<target>/, or image/bundle/<target>/ for manual builds).
export function bundleTarget(platform: NodeJS.Platform = process.platform, arch: string = process.arch): string {
  if (platform === "win32") return "windows-amd64-hyperv";
  if (platform === "darwin") return `darwin-${arch}-vz`; // arm64 today (S2)
  return `linux-${arch}`; // dev-only; no real VM bundle ships for linux
}

// VZ attaches the raw ext4 image directly; HCS wants a VHD (design.md §7).
export function rootfsFileName(platform: NodeJS.Platform = process.platform): string {
  return platform === "win32" ? "rootfs.vhd" : "rootfs.raw";
}

// guestd ships as its own ro volume (not baked into the rootfs) so it can be rebuilt
// without the whole image; attached as a second disk and mounted by init.sh (LABEL=guestd).
// Same disk-format split as the rootfs: HCS wants a VHD, VZ takes raw ext4 (image/build.sh).
export function guestdImageFileName(platform: NodeJS.Platform = process.platform): string {
  return platform === "win32" ? "guestd.vhd" : "guestd.raw";
}

export interface BundleResolveInput {
  optsBundleDir?: string; // constructor option (highest precedence)
  env?: NodeJS.ProcessEnv; // for ATELIER_BUNDLE_DIR (defaults to process.env)
  baseDir: string; // parent dir holding the per-target subdirs
  platform?: NodeJS.Platform;
  arch?: string;
}

// Precedence: opts → env (ATELIER_BUNDLE_DIR) → <baseDir>/<target>.
export function resolveBundleDir(i: BundleResolveInput): string {
  const env = i.env ?? process.env;
  return i.optsBundleDir ?? env.ATELIER_BUNDLE_DIR ?? path.join(i.baseDir, bundleTarget(i.platform, i.arch));
}
