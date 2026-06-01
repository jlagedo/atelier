# Runtime Architecture — UI to In-Guest Agent

| Field | Detail |
|---|---|
| Purpose | Map the current component chain from the desktop UI to the sandboxed in-guest agent. |
| Primary reader | Engineers debugging or changing the WORK-session path. |
| Current agent | `packages/partisan/cli_guest.py` (Python/OpenHands). |
| Historical rationale | [`design.md`](design.md). |

Four long-lived processes carry the product path: renderer, Electron main, Go
broker, and guest VM runner. Each WORK session owns one in-guest partisan child
process.

## Component Map

```text
PROCESS 1 — ELECTRON RENDERER  apps/desktop/src/renderer
  WORK sessions sidebar · chat · workspace file panel
  calls window.atelier.work.{openSession,sendMessage,resume,close,...}
  subscribes window.atelier.work.{onStatus,onEvent,onFiles,onHost}

  Hop 1 — Electron IPC
    preload/preload.ts exposes the narrow contextBridge API
    renderer -> main: ipcRenderer.invoke(IpcChannel.Work*)
    main -> renderer: webContents.send(WorkStatus/Event/Files)

PROCESS 2 — ELECTRON MAIN  apps/desktop/src/main
  sessions/manager.ts owns the WORK state machine and one shared VM, vm0
    ensureVM -> attachWorkspace -> startLoop(partisan --serve) -> execInput
  sessions/store.ts persists resumable transcripts
  host-client/client.ts is the JSON-RPC Hop-2 client

  Hop 2 — local broker IPC
    Windows: named pipe \\.\pipe\atelierd
    macOS/Linux dev: unix socket /tmp/atelierd.sock
    JSON-RPC 2.0 with LSP-style Content-Length framing
    methods: getStatus createVM startVM stopVM exec execInput
      attachWorkspace detachWorkspace readFile writeFile setEgressPolicy setTime

PROCESS 3 — HOST BROKER  services/cmd/atelierd
  broker.Register(...) exposes the Hop-2 method surface
  authorize(method, door) -> Gate + audit log
  vmm.Manager drives the platform Driver:
    macOS: Virtualization.framework / VZ
    Windows: HCS via computecore.dll
  netjail.Allowlist owns live default-deny DNS + TCP egress policy

  Hop 3 control — broker <-> runner
    Windows: AF_HYPERV <-> guest AF_VSOCK
    macOS: VZVirtioSocketDevice <-> guest AF_VSOCK
    port 5000 (vsock.GuestRPCPort), JSON-RPC 2.0 + exec/output notifications

  Hop 3 files — host workspace <-> guest mount
    Windows/HCS: Plan9/9p over hvsock, port 564 or per-session 600+N
    macOS/VZ: one virtio-fs device mounted at /sessions; sessions appear as tags
    Host-side readFile/writeFile still go through the broker path jail

  Hop 3 network — guest gvforwarder <-> host netjail
    guest gvforwarder dials host CID 2 port 1024
    host runs gvisor-tap-vsock stack with DNS allowlist + IP pinning
    no shipped NAT/bridged NIC path

PROCESS 4 — GUEST VM  Linux utility VM, one shared VM named vm0
  runner  services/cmd/runner, PID 1
    JSON-RPC server on AF_VSOCK :5000
    methods: exec execInput mount unmount
    streams child stdout/stderr as exec/output notifications

  gvforwarder
    bridges tap0 to the host netjail over vsock://2:1024

  per exec / per session sandbox
    bwrap curated filesystem view
    seccomp cBPF filter
    uid/gid 1001, all capabilities dropped
    per-exec cgroup limits
    Landlock shim for filesystem + outbound TCP 443 scope

  per WORK session in-guest agent child
    /opt/atelier/packages/partisan/.venv/bin/python cli_guest.py --serve
      --workspace /sessions/<appId> [--resume <conversationId>]
    cwd: /opt/atelier/packages/partisan
    stdin:  NDJSON LoopControl {user|close|export_context}
    stdout: NDJSON LoopEvent {init|text_delta|text|tool_use|policy|tool_result|
      result|turn_done|context|error}
    model calls exit only through the egress jail
```

`packages/artisan` still ships as the TypeScript reference implementation. The
live launch site is partisan.

## One Round-Trip

```text
Renderer  window.atelier.work.sendMessage(appId, "do X")
  -> Hop 1 invoke WorkSendMessage
Main      manager.sendMessage -> host.execInput({
            id:"vm0", sessionId:appId,
            data: base64({type:"user",text:"do X"} + "\n")
          })
  -> Hop 2 JSON-RPC execInput over the pipe/socket
Broker    authorize("execInput","compute") -> DialGuest("vm0", 5000)
  -> Hop 3 JSON-RPC execInput over vsock
runner    writes the NDJSON line into the session child's stdin
partisan  reads the turn -> OpenHands SDK -> model call through netjail
          emits NDJSON LoopEvents on stdout
runner    wraps stdout chunks as exec/output notifications
Broker    relays notifications back over Hop 2
Main      PartisanClient splits NDJSON and emits WorkEvent
Renderer  renders text, tool cards, policy cards, and final result
```

## Protocol Summary

| Hop | Boundary | Transport | Wire format |
|---|---|---|---|
| 1 | Renderer <-> Main | Electron IPC + contextBridge | structured clone messages |
| 2 | Main/CLI <-> Broker | Windows named pipe or unix socket | JSON-RPC 2.0, Content-Length |
| 3-control | Broker <-> runner | AF_HYPERV/VZ vsock, port 5000 | JSON-RPC 2.0 + notifications |
| 3-files | Broker/host <-> guest | Windows 9p or macOS virtio-fs | kernel filesystem mount |
| 3-network | guest <-> Broker | gvisor-tap-vsock over vsock, port 1024 | HTTP /connect into user-mode TCP/IP stack |
| stdio | runner <-> partisan | OS pipes | NDJSON LoopControl/LoopEvent |

## Security Notes

| Gap | Source |
|---|---|
| No shipped general-purpose guest NIC; egress flows through host `netjail` plus the Landlock TCP 443 backstop. | `services/internal/netjail`, `services/cmd/atelier-landlock` |
| Hop 2 still lacks ship-grade access control: Windows pipe has no explicit security descriptor, unix socket lives under `/tmp`, and broker gate is `AllowAll`. | [`../security/ipc-security.md`](../security/ipc-security.md) |
| Model key still enters the in-guest process environment. | [`../security/vm-sandbox.md`](../security/vm-sandbox.md) F-02 |
| Raw guest sandbox audit is evidence only. | [`../security/audits/2026-05-24-vz-guest-assessment.md`](../security/audits/2026-05-24-vz-guest-assessment.md) |

## Key Source References

- Hop 1: `apps/desktop/src/preload/preload.ts`, `apps/desktop/src/main/ipc/handlers.ts`
- Main state machine: `apps/desktop/src/main/sessions/manager.ts`
- Hop 2 client: `apps/desktop/src/main/host-client/client.ts`
- Hop 2 transports: `services/internal/rpc/transport_windows.go`,
  `services/internal/rpc/transport_unix.go`
- Broker and gate: `services/internal/broker/broker.go`, `policy.go`, `audit.go`
- Protocol surface: `packages/protocol/schema/protocol.json`
- VMM drivers: `services/internal/vmm/driver_darwin.go`,
  `services/internal/vmm/driver_windows.go`
- Guest runner and sandbox: `services/cmd/runner/main.go`,
  `services/cmd/runner/sandbox_linux.go`, `services/cmd/atelier-landlock/main.go`
- Egress jail: `services/internal/netjail/network.go`, `services/internal/netjail/filter.go`
- Live in-guest agent: `packages/partisan/cli_guest.py`
- TS reference agent: `packages/artisan/src/cli-guest.ts`
