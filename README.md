# Atelier

A Cowork-style **desktop AI workspace**: a **Go** host service drives a Linux utility VM on
Windows HCS, a **TypeScript** agent loop runs the AI, and an **Electron/React** app is the UI.
The whole point is letting an AI agent work on **local files** safely — by containment, not
per-click consent.

> Full design, decisions, and a glossary live in **[`docs/design.md`](docs/design.md)**; the
> slice-by-slice build order and progress in **[`docs/implementation-plan.md`](docs/implementation-plan.md)**.
> Build/run/test instructions and conventions live in **[`AGENTS.md`](AGENTS.md)**.

## Status

The **compute substrate is real**: our own Go bindings to Windows HCS boot our own Linux VM, and
the host drives commands inside the guest, streaming output back. Work follows the milestone ladder
in [`docs/implementation-plan.md`](docs/implementation-plan.md).

- **Host service** (`services`) — a Go broker speaking JSON-RPC 2.0 (Content-Length framed) over a
  named pipe (Windows) / unix socket (dev). Every capability passes a policy gate (allow/ask/deny)
  + audit log. What works end-to-end:
  - **Boot** — our `computecore.dll` bindings author the compute-system doc and start the VM on a
    self-built, version-matched **kernel + initrd + Ubuntu 22.04 rootfs** bundle.
  - **Compute door** — `exec` runs a command in the guest over hvsocket and streams stdout/stderr
    back live (`vmctl exec -id vm0 -- ls -la /`).
  - **Files door** — a host folder mounts at `/workspace` over **9p**, attachable/swappable on a
    *running* VM (no reboot); `readFile`/`writeFile` are broker-mediated and jailed to the
    workspace (rejects `..` and escaping symlinks).
  - **Network door** — a no-NIC user-mode network (gvisor-tap-vsock) with a default-deny egress
    allowlist + pinning DNS resolver; `setEgressPolicy` swaps the allowlist at runtime. *Code
    complete, not yet live-verified.*
- **Desktop shell** (`apps/desktop`) — a hardened Electron + React 19 + Tailwind v4 app booting a
  chat-forward **mock** layout, with a working IPC seam. Not yet wired to the broker; the real
  agent loop and Electron-over-broker UI are the final milestones (M5–M6).

## Layout

| Dir | What |
|---|---|
| `apps/desktop` | Electron/React desktop UI (chat-forward shell) |
| `services` | Go module — privileged host broker, in-VM daemon, dev CLI |
| `packages` | Shared TS libs — agent loop, protocol, provider seam, UI *(skeleton)* |
| `image` | VM image build pipeline — kernel + initrd + rootfs bundle |
| `skills` | Skill distribution (DXT/`.mcpb` analog) *(skeleton)* |
| `tools/protogen` | Protocol codegen — schema → TS + Go |
| `docs` | Design & architecture docs |

## Quick start

```sh
# Desktop app (TypeScript / Electron)
cd apps/desktop && npm install && npm start     # headless: xvfb-run -a npm start

# Host service (Go) — dev end-to-end over a unix socket
cd services
go run ./cmd/host  -addr /tmp/atelier-host.sock &
go run ./cmd/vmctl -addr /tmp/atelier-host.sock getStatus
```

Driving a real VM needs **Windows + HCS** (Hyper-V Administrators or elevation) and a built image
bundle. Once the broker (`cmd/host`) is up, `vmctl` exercises the whole substrate from a terminal:

```sh
vmctl createVM -id vm0 -kernel vmlinuz -initrd initrd -rootfs rootfs.vhd
vmctl startVM  -id vm0
vmctl exec     -id vm0 -- python3 --version          # run in the guest, stream output back
vmctl attachWorkspace -id vm0 -path C:\work\folder   # share a host folder at /workspace
vmctl setEgressPolicy -allow pypi.org,files.pythonhosted.org
vmctl stopVM   -id vm0
```

Build the VM image bundle (kernel + initrd + rootfs) from `image/` — see `image/build.sh` (runs in
WSL). See **[`AGENTS.md`](AGENTS.md)** for the full command set, conventions, and verification notes.

## Architecture at a glance

```
Renderer (React, sandboxed)
   │  Hop 1: Electron IPC
Main process (Node) ── conductor
   │  Hop 2: named pipe / unix socket — JSON-RPC 2.0 (Content-Length)
Go host service ── broker (policy + audit) + HCS driver  ·  privileged
   │  Hop 3: hvsocket — control / exec / file planes
Linux utility VM ── kernel + rootfs + python + tools
```

> Versions: this scaffold uses latest-stable libraries, which diverges from the design doc's
> Cowork version pins (§11). See `AGENTS.md`.
