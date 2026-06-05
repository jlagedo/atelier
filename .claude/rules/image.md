---
paths:
  - "image/**"
---

# VM image build — `image/`

Builds the utility-VM bundle: kernel + initrd + ext4 rootfs VHD. Sources are tracked under
`image/{rootfs,initrd,kernel,guest}`; output goes to `image/bundle/` (gitignored). The matched
kernel, `/lib/modules`, and boot initramfs all come from one Ubuntu 24.04 Docker build, which also
cross-compiles `gvforwarder`. Multi-GB VHDs are not committed — produced here, stored externally.

## The runner volume (runner + agent, not in the rootfs)

`runner` and the in-guest agent ship together on one ro ext4 volume (`runner.raw` for VZ / `runner.vhd`
for HCS, `LABEL=runner`) — runner at `/opt/runner/atelier-runner`, the agent at `/opt/atelier` —
mounted as a second disk at `/opt` (then runner exec'd) by `image/guest/init.sh`. `image/build.sh
runner` builds it: `stage_agent_ctx` assembles a Docker context from `packages/{protocol,partisan}`
source, and `image/agent/Dockerfile` runs `uv sync` for the target arch (`--platform linux/amd64` or
`linux/arm64`).

Fast dev loop: rebuild only the volume (compile runner + agent `uv sync` + `mke2fs`, no rootfs
export/apt/kernel) and reboot, instead of the whole image. `init.sh` lives in the rootfs, so changes
to it need a full `--image` rebuild, not just the volume. `createVM` carries the host path
(`runnerImagePath`); `atelierctl createVM -runner <img>` and the desktop Session Manager both supply
it from the bundle. `make all` / `build:all` include it automatically.

## Targets + commands

A build `TARGET` (default `windows-amd64-hyperv`) selects guest arch + Docker platform + GOARCH + disk
format + per-target output dir; output goes to `<base>/<target>/`, where `<base>` is `bundle` by
default or `$ATELIER_OUT_BASE` when set (the orchestrator passes `../build/<config>/image`).

```sh
cd image
./build.sh check        # tool readiness + resolved target profile (docker, mke2fs, qemu-img)
./build.sh rootfs       # docker export -> ext4 (mke2fs -d, no root) -> VHD/raw
make all                # Windows: kernel+rootfs+initrd+bundle -> bundle/windows-amd64-hyperv/{vmlinuz,initrd,rootfs.vhd}
make darwin             # macOS arm64 (raw ext4)              -> bundle/darwin-arm64-vz/{vmlinuz,initrd,rootfs.raw}
```
