import path from "node:path";
import { describe, expect, it } from "vitest";
import { defaultHostAddress } from "../host-client";
import { bundleTarget, resolveBundleDir, rootfsFileName } from "./bundle";

describe("bundle resolution", () => {
  it("maps platform/arch to the build TARGET subdir", () => {
    expect(bundleTarget("darwin", "arm64")).toBe("darwin-arm64-vz");
    expect(bundleTarget("win32", "x64")).toBe("windows-amd64-hyperv");
  });

  it("picks the disk format the hypervisor wants", () => {
    expect(rootfsFileName("darwin")).toBe("rootfs.raw"); // VZ attaches raw ext4
    expect(rootfsFileName("win32")).toBe("rootfs.vhd"); // HCS wants a VHD
  });

  it("resolves the macOS bundle dir under the base", () => {
    const dir = resolveBundleDir({ baseDir: "/b", platform: "darwin", arch: "arm64", env: {} });
    expect(dir).toBe(path.join("/b", "darwin-arm64-vz"));
  });

  it("keeps the Windows default intact", () => {
    const dir = resolveBundleDir({ baseDir: "/b", platform: "win32", arch: "x64", env: {} });
    expect(dir).toBe(path.join("/b", "windows-amd64-hyperv"));
  });

  it("honours precedence: opts > ATELIER_BUNDLE_DIR > computed default", () => {
    const env = { ATELIER_BUNDLE_DIR: "/from/env" };
    // opts wins over both env and the computed default.
    expect(resolveBundleDir({ baseDir: "/b", platform: "darwin", optsBundleDir: "/from/opts", env })).toBe(
      "/from/opts",
    );
    // env wins over the computed default.
    expect(resolveBundleDir({ baseDir: "/b", platform: "darwin", arch: "arm64", env })).toBe("/from/env");
  });
});

describe("host address default", () => {
  it("is a named pipe on Windows and a unix socket elsewhere", () => {
    expect(defaultHostAddress("win32")).toBe(String.raw`\\.\pipe\atelierd`);
    expect(defaultHostAddress("darwin")).toBe("/tmp/atelierd.sock");
    expect(defaultHostAddress("linux")).toBe("/tmp/atelierd.sock");
  });
});
