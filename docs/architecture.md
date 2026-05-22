# Atelier Architecture — UI to In-Guest Agent

This document maps every component from the desktop UI down to the agent running
inside the sandbox VM, with the communication protocols and concrete names/ports
used in the codebase.

Four processes, three hops (plus the guestd↔agent stdio channel).

## Component & protocol map

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ PROCESS 1 — ELECTRON RENDERER (sandboxed, React UI)         apps/desktop/...   │
│   WORK sessions sidebar · chat · workspace file panel                          │
│   calls window.atelier.work.{openSession,sendMessage,resume,close,...}         │
│   subscribes window.atelier.work.{onStatus,onEvent,onFiles,onHost}             │
└───────────────────────────────────┬────────────────────────────────────────--┘
                                     │
        ╔════════════════════════════════════════════════════════════╗
        ║ HOP 1 — Electron IPC  (built into Electron)                 ║
        ║   contextBridge "atelier" bridge  (preload/preload.ts)      ║
        ║   renderer→main : ipcRenderer.invoke(IpcChannel.Work*)      ║
        ║   main→renderer : webContents.send(WorkStatus/Event/Files)  ║
        ╚════════════════════════════════════════════════════════════╝
                                     │
┌───────────────────────────────────┴───────────────────────────────────────--─┐
│ PROCESS 2 — ELECTRON MAIN  (Node, unprivileged)            apps/desktop/...    │
│   ipc/handlers.ts  registerIpcHandlers()  ── typed ipcMain.handle             │
│   sessions/manager.ts  SessionManager  (WORK state machine, ONE shared VM vm0)│
│       ensureVM → attachWorkspace → startLoop(cli-guest --serve) → execInput   │
│   sessions/store.ts  SessionStore (durable transcripts/hibernate)             │
│   host-client/client.ts  PipeClient  ── JSON-RPC 2.0 client                   │
│                                                                                │
│   (a second Hop-2 client exists: the Go CLI  cmd/vmctl  for dev/testing)      │
└───────────────────────────────────┬───────────────────────────────────────--─┘
                                     │
        ╔════════════════════════════════════════════════════════════╗
        ║ HOP 2 — Named pipe  \\.\pipe\atelier-host  (go-winio)       ║
        ║   JSON-RPC 2.0, LSP-style Content-Length framing            ║
        ║   methods (pkg/protocol): getStatus createVM startVM stopVM ║
        ║     exec execInput attachWorkspace detachWorkspace          ║
        ║     readFile writeFile setEgressPolicy                      ║
        ║   ⚠ pipe ACL/security-group not yet set (default DACL)      ║
        ╚════════════════════════════════════════════════════════════╝
                                     │
┌───────────────────────────────────┴───────────────────────────────────────--─┐
│ PROCESS 3 — HOST BROKER  cmd/host  (Go, ships as LocalSystem service)          │
│   rpc.Server  →  broker.Register(...)                                          │
│   authorize(method, door) ── Gate (policy.go) + audit log (audit.go)          │
│        doors: compute | files | network                                       │
│   vm.Manager ── HCS via hcsshim/own bindings (Create/ModifyComputeSystem)     │
│   netjail.Allowlist ── default-deny egress policy (live, no reboot)           │
└───────┬───────────────────────────┬───────────────────────────────┬─────────┘
        │ CONTROL plane             │ FILES door                    │ NETWORK door
        │                           │                               │
 ╔══════╧═════════════╗   ╔═════════╧════════════╗      ╔═══════════╧═══════════╗
 ║ HOP 3 ctrl         ║   ║ HOP 3 files          ║      ║ HOP 3 net             ║
 ║ AF_HYPERV ⇄ vsock  ║   ║ Plan9 / 9p           ║      ║ user-mode network     ║
 ║ port 5000          ║   ║ default port 564     ║      ║ over vsock            ║
 ║ (GuestRPCPort)     ║   ║   tag "workspace"    ║      ║ guest gvforwarder →   ║
 ║ svcGUID=           ║   ║ per-session: port    ║      ║ AF_VSOCK CID2 :1024   ║
 ║ VsockServiceID(5k) ║   ║ 600+N, tag=<appId>   ║      ║ (EgressLinkPort) →    ║
 ║ JSON-RPC 2.0       ║   ║ guest mounts at      ║      ║ host gvisor-tap-vsock ║
 ║ host DialGuest()   ║   ║ /sessions/<appId>    ║      ║ DHCP/DNS/forward +    ║
 ║                    ║   ║                      ║      ║ allowlist (egress)    ║
 ╚══════╤═════════════╝   ╚═════════╤════════════╝      ╚═══════════╤═══════════╝
        │                           │                               │
┌───────┴───────────────────────────┴───────────────────────────────┴──────────┐
│ PROCESS 4 — GUEST VM  (Hyper-V, Linux; ONE shared VM "vm0")                    │
│                                                                                │
│   guestd  (cmd/guestd, PID 1 / init)  ── AF_VSOCK JSON-RPC server :5000       │
│       methods: exec · execInput · mount · unmount                             │
│       streams child stdout/stderr as  "exec/output"  notifications (base64)   │
│                                                                                │
│   ├─ mounts host shares at /sessions/<appId>  (9p, trans=fd over vsock)       │
│   ├─ gvforwarder ── bridges tap0 → host network (the only egress path)        │
│   └─ spawns, per session, the IN-GUEST AGENT as a child (stdin/stdout pipe):  │
│                                                                                │
│      ┌──────────────────────────────────────────────────────────────────┐    │
│      │ IN-GUEST AGENT  packages/agent  src/cli-guest.ts (tsx)            │    │
│      │   launched: tsx cli-guest.ts --serve --workspace /sessions/<id>   │    │
│      │             [--resume <sdkSessionId>]                             │    │
│      │   persistent loop on the Claude Agent SDK                         │    │
│      │   STDIN  ← NDJSON LoopControl : {user|close|export_context}       │    │
│      │   STDOUT → NDJSON LoopEvent : init text tool_use tool_result      │    │
│      │            policy result turn_done context                        │    │
│      │   model calls → api.anthropic.com:443  (via gvforwarder→egress)   │    │
│      └──────────────────────────────────────────────────────────────────┘    │
└────────────────────────────────────────────────────────────────────────────--┘
```

## One round-trip: user sends a chat message

```
Renderer  window.atelier.work.sendMessage(appId, "do X")
   │  Hop1: ipcRenderer.invoke(WorkSendMessage)
Main     manager.sendMessage → host.execInput({id:"vm0", sessionId:appId,
   │                              data: base64( {type:"user",text:"do X"}\n )})
   │  Hop2: JSON-RPC "execInput" over \\.\pipe\atelier-host
Broker   authorize("execInput","compute") → DialGuest("vm0")
   │  Hop3: JSON-RPC "execInput" over AF_HYPERV :5000
guestd   writes the NDJSON line into that session's child STDIN (matched by sessionId)
Agent    loop reads turn → calls Claude (api.anthropic.com via egress jail)
   │     emits NDJSON LoopEvents on STDOUT
guestd   each stdout chunk → "exec/output" notification (base64)
   │  Hop3 → Broker relays verbatim → Hop2 notification
Main     execStream.onOutput → split NDJSON lines → emit.event(appId, ev)
   │  Hop1: webContents.send(WorkEvent)
Renderer onEvent(...) renders text/tool_use/turn_done
```

## Protocol summary

| Hop | Boundary | Transport | Wire format | Who designed it |
|-----|----------|-----------|-------------|-----------------|
| 1 | Renderer ⇄ Main | Electron IPC (`ipcRenderer`/`ipcMain` + `contextBridge`) | structured-clone msgs on `IpcChannel.*` | built into Electron |
| 2 | Main ⇄ Broker | Named pipe `\\.\pipe\atelier-host` (go-winio) | JSON-RPC 2.0, Content-Length framing | you |
| 3-ctrl | Broker ⇄ guestd | AF_HYPERV⇄AF_VSOCK, port **5000** | JSON-RPC 2.0 (+ `exec/output` notifications) | you |
| 3-files | Broker ⇄ guest | Plan9/9p, port **564** / per-session **600+N** | 9p over vsock (`trans=fd`) | HCS + you |
| 3-net | guest ⇄ Broker | user-mode net over vsock, port **1024** | gvisor-tap-vsock (DHCP/DNS/forward) + allowlist | you |
| stdio | guestd ⇄ Agent | OS pipe (child stdin/stdout) | NDJSON `LoopControl`/`LoopEvent` | you |

## Security notes

- The agent's **only** way out is the Network door (gvforwarder → host allowlist,
  default-deny → `api.anthropic.com:443`).
- The agent's instructions/turns arrive **only** as NDJSON on stdin from guestd;
  the agent itself never gets an ambient connection to the broker.
- Today the broker is reachable only via Hop 2 (the named pipe); there is **no**
  guest→broker control channel. Adding one (Topology B) is the surface that needs
  careful, default-deny, origin-aware gating.

## Key source references

- Hop 1: `apps/desktop/src/preload/preload.ts`, `apps/desktop/src/main/ipc/handlers.ts`
- Main state machine: `apps/desktop/src/main/sessions/manager.ts`
- Hop 2 client: `apps/desktop/src/main/host-client/client.ts`
- Hop 2 transport: `services/internal/rpc/transport_windows.go` (`DefaultAddress`)
- Broker + gate: `services/internal/broker/broker.go`, `policy.go`, `audit.go`
- Method/protocol surface: `services/pkg/protocol/protocol.go`
- Hop 3 control plane: `services/internal/vm/guest_windows.go` (`DialGuest`),
  `services/cmd/guestd/main.go`, `services/internal/vsock/vsock.go` (ports)
- In-guest agent: `packages/agent/src/cli-guest.ts`
