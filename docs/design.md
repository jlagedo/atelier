# Atelier — Design Document

> **Name:** **Atelier** — a workshop where a craftsperson works on their own materials, in their own space; fitting for a contained, local AI workspace. *(Was "theparser", a throwaway working name.)*
>
> **Status:** Design and decision record. The code now implements the main Topology B path; use
> [`implementation-status.md`](implementation-status.md) for build history and
> [`runtime-architecture.md`](runtime-architecture.md) for the concrete process/protocol map.
> **Stack:** **Go** (host broker / HCS driver, with thin `computecore.dll` bindings) +
> **TypeScript/Node** (agent loop via `@anthropic-ai/claude-agent-sdk`) + **Electron/React** (UI).
> See §8.
> **Last updated:** 2026-05-22

---

## Contents

**Part I — Problem & Principles**
1. Vision & Why
2. Security Model — *containment, not consent*
3. Scope Boundaries

**Part II — Reference (what already exists)**
4. Reference Architecture: Anthropic **Cowork** (verified)
5. HCS & the Windows virtualization stack

**Part III — Our Architecture (the decisions)**
6. Compute Sandbox decision — **3b, dedicated HCS utility VM**
7. The Linux VM image — Kernel + Rootfs
8. Process Architecture (hops · protocol · transport · languages · topology)
9. Privilege / Elevation Model
10. The Three Doors — detail
11. UI / Frontend Stack
12. Skills (distribution)
13. Provider Seam & Data Residency

**Part IV — Plan**
14. Milestone Ladder M0–M6

**Back matter**
15. References
16. Open Questions / Pending Decisions
17. Glossary / Keyword Dictionary

---

# Part I — Problem & Principles

## 1. Vision & Why

An **Electron desktop app**: a chat client for an internal AI stack (**BNY Eliza AI**) that lets **non-technical operations users** run **skills and agents safely** against the company AI — and, crucially, **work directly on their local files**.

### The problem we're solving
Ops users already have a great **browser-based AI agent** with company-wide data access and subagents. What it *can't* do is touch local files without friction:

> upload a document → wait for processing → download result → work on it → re-upload → repeat…

A desktop "workspace" app removes that treadmill: the agent reads/generates/updates local CSVs, Excel, and documents **in place**, no upload/download dance.

### The key architectural consequence
**Local file access is the entire reason this app exists.** The browser agent already covers company-wide data. So any design where the desktop does *not* have first-class local capability defeats the purpose. This single fact drives every decision below.

### Two goals (be honest about both)
- **Personal (the real driver):** learn to build an Electron desktop app, and understand **Cowork-grade VM isolation on Windows** at a deep level.
- **Work (the framing):** a prototype/proposal for BNY to give ops users safe access to the internal AI stack.

---

## 2. Security Model — *containment, not consent*

### Two layers people conflate
1. **App hardening (standard Electron).** Protects the *app* from malicious content. Table-stakes: `sandbox: true`, `contextIsolation: true`, no `nodeIntegration` in the renderer, strict CSP, a small **allowlisted IPC** surface. Not the hard part.
2. **Agent containment (the hard part).** The LLM's output is **untrusted input**. A prompt injection or hallucination can turn a "run code" / "write file" tool into damage. App hardening won't save you — the app is working as designed. Containment means the agent has **no ambient authority**: it acts only through explicitly granted, individually-sandboxed **capabilities**, each policy-gated (`allow`/`ask`/`deny`) and **audited**.

### Why not just copy Claude Desktop?
Classic Claude Desktop (MCP) is a **trust + consent** model:
- MCP servers run as **full-privilege child processes**.
- The filesystem server does **app-level path checks**, not OS sandboxing.
- Safety = **per-tool-call user approval** + the user having self-selected servers.

That's fine for *developers*. It's wrong for **non-technical bank ops** who install skills from a registry and **rubber-stamp every "Allow"**. We need containment **by construction**, not consent.

### The "three doors" capability model *(full detail in §10)*
The agent has exactly three doors; each is independently sandboxed and audited:

| Door | Capability | Containment |
|---|---|---|
| **Files** | read/write in the workspace | jailed to the workspace folder; writes need approval |
| **Network** | call company APIs | only via connected **MCP servers** (egress allowlist) |
| **Compute** | run Python | runs **inside the VM**; no FS/net except what the host bridges |

Nice side effect: because the compute door has **no direct network**, the agent can't hide exfiltration inside opaque Python — anything touching the network is forced through the **audited MCP layer**.

---

## 3. Scope Boundaries

- Desktop app = **local files + AI** (+ MCP to company APIs later). Company-wide data stays the **browser agent's** lane (for now).
- With the 3b VM (§6), **big-CSV Python is in scope** (real Python in the VM), unlike the earlier Pyodide-limited plan.

---

# Part II — Reference (what already exists)

## 4. Reference Architecture: Anthropic **Cowork** (verified on the dev machine)

**Cowork** (Anthropic, Jan 2026; Windows Feb 10 2026) is essentially the consumer version of this project: a Claude Desktop agent for **non-technical users** that works in local files ("Claude Code for people who don't code"). This is great news — the concept is validated, and we have a proven architecture to study.

> **Pitch reframe:** we're not inventing a category. We're enterprise-izing a validated one — *"Cowork, but pointed at BNY Eliza, with our auth, audit, and a skills registry."*

### How Cowork sandboxes (defense in depth)
- **Hard isolation:** a **full local Linux VM** via **HCS** (Host Compute System) — *not* a normal Hyper-V Manager VM (see §5).
- **Process sandbox (inside the VM):** **bubblewrap** (FS view, namespaces, capabilities) + **seccomp** (syscall filtering).
- **Network:** all VM traffic via a **local proxy with a strict allowlist** (can `pip`/`npm`, can't make arbitrary HTTP).
- **Files:** the designated folder is shared host↔VM via **Plan9/9p** (verified — see §8; some docs mention VirtioFS, but the shipped Windows build uses 9p).
- **Tools:** MCP servers are **passed through** into the VM.

### Empirical confirmation (probed on this Windows 11 Pro dev box)
Everything below was observed directly — we reverse-engineered Cowork's model from real artifacts:

- **Privileged broker service exists:**
  ```
  Name      : CoworkVMService
  State     : Running
  StartName : LocalSystem                 ← runs as SYSTEM (privileged)
  PathName  : ...\Claude_1.8089...\app\resources\cowork-svc.exe
  ```
  → The "elevated process" is a **service** (`cowork-svc.exe`, LocalSystem), **not** a "Claude" process. That's why a scan for an elevated Claude process finds nothing.

- **The unprivileged user genuinely can't touch HCS:**
  ```
  hcsdiag list  →  "Insufficient privileges. Only administrators or members of the
                    Hyper-V Administrators group are permitted to access VMs..."
  ```
  → Proves the app (running as the user) **cannot call HCS directly**; it *must* go through the SYSTEM service.

- **The invisible VM, made visible (elevated `hcsdiag list`):**
  ```
  cowork-vm-81b3ce48   VM,   Running,   66DA4C0A-8CA5-53E6-BC28-7D8305F4B071   cowork-vm-81b3ce48
        │               │       │                  │
     name/ID          type    state         runtime GUID
  ```
  → Type **`VM`** (not `Container`) = strongest isolation tier. Invisible to Hyper-V Manager because it's **HCS-managed (not VMMS)**.

- **VM internals (from `python --version` / `uname` inside Cowork):**
  - Python **3.10.12**, Ubuntu **22.04.5 LTS**, kernel **6.8.0-106-generic**, x86_64.
  - Kernel is **stock Ubuntu `-generic`**, *not* WSL2's `-microsoft-standard-WSL2`. → Cowork ships its **own kernel + Ubuntu rootfs** in a **dedicated** utility VM; it is **not reusing WSL2**.

- **Hyper-V infra processes present:** `vmcompute` (HCS service), `vmms` (VM mgmt), `vmwp.exe` (per-VM Worker Process hosting the Ubuntu VM).

### The confirmed privilege chain
```
claude.exe (you, unprivileged)
   → named pipe → cowork-svc.exe (LocalSystem)
       → vmcompute (HCS, SYSTEM)
           → vmwp.exe (hosts the Ubuntu VM)
```

---

## 5. HCS & the Windows virtualization stack

"Hyper-V" is a hypervisor with **several management surfaces**; only one shows up in Hyper-V Manager:

| Surface | API / process | In Hyper-V Manager? | Used by |
|---|---|---|---|
| Full Hyper-V role | VMMS (`vmms.exe`) | **Yes** | hand-made VMs |
| **Host Compute System (HCS)** | `vmcompute.exe` / `computecore.dll`, `HcsCreateComputeSystem` | **No** | **WSL2, Windows Sandbox, Docker, Cowork** |
| Windows Hypervisor Platform (WHP) | `WinHvPlatform.dll`, `WHvCreatePartition` | **No** | VirtualBox, QEMU, Android emulator |

We are building on **HCS** — the same machinery WSL2/Cowork use, which is why our VM will likewise be **invisible to Hyper-V Manager**.

---

# Part III — Our Architecture (the decisions)

## 6. Compute Sandbox decision — **3b, dedicated HCS utility VM** (Cowork parity)

### The ladder we considered
| Rung | Python runs in | Isolation | Effort | Verdict |
|---|---|---|---|---|
| 1. Pyodide (WASM, in-app) | the app | by construction (no FS/net) | low | rejected — memory/package limits |
| 2/3a. Reuse WSL2 (hardened distro) | existing WSL2 VM | moderate (must lock down) | medium | rejected — shares subsystem, weaker isolation |
| **3b. Dedicated HCS utility VM** | own VM + bubblewrap/seccomp + egress proxy | **hard (Cowork-grade)** | high | **CHOSEN** |

### Rationale
- Enterprise-grade isolation is a hard requirement for the bank.
- The personal learning goal **is** "understand exactly how Cowork works."
- Real Python + full ecosystem + real performance handles big-CSV / scipy workloads that Pyodide can't.

### Acknowledged tradeoff
This shifts the center of gravity **from Electron to Windows virtualization systems programming**. The Electron app becomes the **final** milestone (M6), not the first. Accepted knowingly.

---

## 7. The Linux VM image — Kernel + Rootfs

We do **not** build Linux from scratch — we borrow both pieces and supply them directly (lightweight, LCOW-style, no installer/bootloader).

- **Kernel** — the engine; one file (`vmlinux`/`vmlinuz`), direct-booted (KernelDirect). Needs virtio / 9p / virtiofs / vsock / Hyper-V drivers. **Borrow** Microsoft's LCOW/WSL2 kernel (has them all), or a generic distro kernel. (Cowork ships its own Ubuntu generic kernel.)
- **Rootfs** — the userland ("the distro"). Grab a minimal distro off the shelf: `docker export` of `debian:slim`/`ubuntu`, or a WSL rootfs tarball. Pour into an **ext4** image. (Alpine considered and rejected — see below.)

### Boot sequence (it's just a Linux boot, with one twist)
```
1. Host (HCS) loads the kernel directly        ← replaces BIOS/GRUB ("KernelDirect")
2. Kernel starts, brings up virtio drivers, sees the virtual disk
3. Kernel mounts the rootfs disk as /
4. Kernel runs PID 1 (init) from the rootfs    ← eventually our guest agent + Python live here
```
An **initramfs** is a tiny in-RAM temporary rootfs used before pivoting to the real one — required when the kernel loads its storage/transport drivers as modules (decided: **yes**, see spec below).

**Everything *inside* the VM is ordinary Linux** (familiar territory). The only genuinely new continent is the **host side**: driving HCS + the host↔guest plumbing.

### VM image spec (decided — the M1 target)

**Kernel — reuse a prebuilt one (do NOT hand-compile). *Which* one is TO BE VERIFIED at M1.** A utility VM is a **Hyper-V guest**, so the kernel needs Hyper-V/VMBus drivers.

> ⚠️ **Correction — do not assume the WSL2 kernel.** The only kernel we *know* boots an HCS utility VM is the **LCOW kernel that ships with hcsshim/the tooling** (that's literally what it boots). hcsshim's UVM config takes `KernelPath` *(required)* + `InitRDPath` *(optional)*. Whether the **WSL2 kernel** boots *our* custom rootfs in a non-WSL VM is plausible but **unverified**. And per our own evidence, **Cowork runs a stock `6.8.0-106-generic` Ubuntu kernel — NOT a WSL2 kernel.**

**Kernel ↔ initramfs are coupled** (this is the real decision):
- Drivers **built-in (`=y`)** → direct-boots a VHD root with **no boot initramfs**. (LCOW/WSL2 kernels are built this way.)
- Drivers as **modules (`=m`** — the generic Ubuntu case) → **needs a boot initramfs** to load `hv_storvsc`/virtio *before* mounting root. (So Cowork's generic kernel almost certainly ships an initrd — confirmed by the on-disk bundle below.)

**Plan (decided — keep kernel & userland matched):**
- **M0 — disposable bootstrap:** boot the tooling's **own matched LCOW kernel + rootfs pair** (known-good) *only* to prove HCS works on the box. The LCOW kernel **never** marries our Ubuntu userland.
- **M1+ — the real, matched image:** **generic Ubuntu kernel + matching initramfs + Ubuntu rootfs**, all the same version (so `/lib/modules/<ver>` matches the running kernel). This is the coupling instinct *and* Cowork's actual choice.

> **Why coupled (the shiver, explained):** Linux's syscall ABI means any userland runs on any modern kernel (WSL2 does this daily) — *but* `/lib/modules`, `modprobe`, and DKMS only work when the kernel matches the userland. A mismatched kernel = **no working module ecosystem**. WSL2 lives with that; we won't.

**Boot/cmdline:** direct kernel boot (KernelDirect); root on an **ext4 VHD** (hcsshim `PreferredRootFSType = vhd`); `console=hvc0 root=<disk> rootfstype=ext4 …`. *(A boot initrd is also required per the coupling above — that's separate from rootfs-as-VHD.)*

**Must-have drivers** (whichever kernel): Hyper-V VMBus (`CONFIG_HYPERV`, `hv_storvsc`, `hv_netvsc`, **`hv_sock`**), virtio (blk/scsi + **virtio-fs**), 9p (`9pnet_virtio`), **ext4**, FUSE, overlayfs.
- **Bonus:** LCOW/WSL2 kernels ship **9p (and recent builds, virtiofs)** → keeps the file-share choice (§16) open for free.

**Rootfs — Ubuntu 22.04 (glibc); reject Alpine.** Deciding factor = **Python wheels**: Alpine/musl forces source compiles for many wheels; glibc means `pip install` just works. Also mirrors Cowork.
- **Pick:** **Ubuntu 22.04 LTS** userland. (Debian 12-slim = leaner glibc alternative.)
- **Source:** `docker export ubuntu:22.04` → **ext4 VHD(X)**. (Ubuntu base / WSL rootfs tarball also fine.)
- **Manifest:** init (busybox/`sh` to start), **python3 + pip**, **node** (Topology-B agent), git, ripgrep, ca-certificates; later the **guest bridge daemon** + **TS agent CLI**.
- **Layout:** base **read-only** + **ephemeral overlay** (overlayfs/tmpfs, per-session) + **workspace** as the only persistent mount (9p/virtiofs). Arch **x86_64**.

**Sequence:** M0 boots whatever the LCOW tooling ships (don't decide). M1 wires *this* spec into your own `HcsCreateComputeSystem` call.

### Empirical confirmation — Cowork's actual bundle on disk

Found at `%APPDATA%\Claude\vm_bundles\claudevm.bundle\` — **this is exactly the layout this section specifies**, which is strong validation:

| File | Size | What |
|---|---|---|
| `vmlinuz` | 14.3 MB | the kernel (direct-booted; `.vmlinuz.origin` present) |
| `initrd` | 169 MB | the boot initramfs (confirms the kernel loads drivers as modules → initrd required) |
| `rootfs.vhdx` | ~9 GB | the Ubuntu userland + the in-guest `cowork-daemon` (ext4 in a VHDX) |
| `sessiondata.vhdx` | 548 MB | per-session ephemeral disk (matches the overlay/ephemeral layer idea) |
| `smol-bin.vhdx` | 36 MB | small aux VHD (also shipped in install as `smol-bin.x64.vhdx`; likely a minimal boot/agent disk) |

All three of `vmlinuz`/`initrd`/`rootfs.vhdx` carry a `.origin` marker with the **same content hash** (`5680b11b…`) → the kernel + initrd + rootfs are **versioned and shipped together as one pinned bundle**. Adopt that discipline (don't mix-and-match versions — the coupling rule, enforced operationally).

---

## 8. Process Architecture

```
Renderer (Chromium UI, sandboxed JS)
        │  Hop 1: Electron IPC (ipcRenderer ⇄ ipcMain) — built into Electron
Main process (Node) ── conductor/session manager; Topology A CLI can run HERE
        │  Hop 2: named pipe, JSON — you design this
Go service ── HCS driver (own computecore bindings) + BROKER (policy/audit · files/net/compute)
        │  Hop 3: hvsocket/vsock · HCS · 9p — you design this
Linux utility VM ── kernel + rootfs + Node + python + tools
                    in Topology B the agent loop (TS) lives HERE
```

**Three hops, but you only *design two*:** Hop 2 (Node ⇄ Go, named pipe) and Hop 3 (host ⇄ guest, hvsocket). Hop 1 is built into Electron. Don't conflate them.

**The broker is the containment.** The agent never drives the service directly — its requests pass through the broker's **policy gate (allow/ask/deny) + audit log** first. Without that gate you've rebuilt Claude Desktop's rubber-stamp problem.

### Native code: sidecar, not in-process addon
| Option | Verdict |
|---|---|
| **Option 1 — in-process (cgo `c-shared` lib via N-API/FFI)** | ❌ awkward for Go *and* a crash kills the whole app; would force the entire GUI to run elevated (security anti-pattern). |
| **Option 2 — standalone Go service/sidecar** | ✅ **CHOSEN** — the natural Go shape; crash isolation, privilege separation, and (key for learning) the whole VMM is developable/testable from a **terminal** with no Electron until M6. |

*(These "Options" are about the native-code boundary — not to be confused with **Topology A/B**, which is about where the agent loop runs. See below.)*

This mirrors the grown-ups: Docker Desktop (UI + privileged engine), WSL (`wsl.exe` + `wslservice`) — and **Docker's engine + hcsshim are themselves Go**, so we're in good company.

### Protocol (Hop 2) — borrow Cowork's exact design (verified)

Reverse-engineered from the shipped app's `index.js` (the main-process bundle) + live named-pipe enumeration. **Cowork's host-broker IPC is a clean, standard design — adopt it almost verbatim.**

| Aspect | Cowork (verified) | Our choice |
|---|---|---|
| Transport | named pipe **`\\.\pipe\cowork-vm-service`** (broker) + per-VM **`cowork-daemon-console-<vmid>`** (guest console) | named pipe (match) |
| Wire format | **JSON-RPC 2.0** — request (`method`+`id`), **notification** (`method`, no `id`) for streaming, response (`id`+`result`\|`error`) | JSON-RPC 2.0 (match) |
| Framing | **`Content-Length`** headers (LSP/DAP style) | Content-Length framing — *(supersedes the earlier "newline-delimited JSON" idea; survives embedded newlines in file content / streamed stdout)* |
| Validation | every message **Zod-validated**, `.strict()` (MCP-SDK primitives) | Go: typed structs + `encoding/json` · Node: Zod (or reuse MCP SDK's JSON-RPC types) |
| Observability | **OpenTelemetry**-traced (`rpc.jsonrpc.*` attrs per call) | add OTel/structured tracing later |
| Methods (broker) | `createVM`, `startVM`, `stopVM`, `getStatus`, `readFile`, `writeFile` (lifecycle + **file passthrough**) | same taxonomy + our `exec`/policy/approval/audit verbs |

**Two design lessons baked into their choice:**
1. **Streaming = JSON-RPC notifications.** Commands are requests; streamed stdout/progress/logs are notifications (no `id`). One channel, no second socket.
2. **The broker mediates file I/O itself** (`readFile`/`writeFile` are *broker* methods, not the agent touching disk). → the **file jail is enforced at the privileged boundary**, in the Go service. Consciously adopt this (ties to §10 Files).

**Privilege split confirmed:** the JS client has **zero** HCS/hvsocket code — it's a thin RPC client; *all* privileged HCS + guest transport lives in the compiled `cowork-svc.exe`. → Validates Option 2 above: keep Node dumb (pure RPC client), put everything privileged in the Go service.

### Transport (Hop 3) — host ↔ guest, verified

Reverse-engineered from the Go symbol table of **`cowork-svc.exe`** (package `github.com/anthropics/cowork-win32-service` — note: **this repo is private/proprietary**, 404 on GitHub; only its *open-source dependencies* are public). **The service is written in Go** — and this evidence is *why we chose Go too* (see the decision box below). The host↔guest link is **not one protocol — it's three layered channels**, brought up in this order:

| # | Channel | Implementation (verified symbols) | Role |
|---|---|---|---|
| 1 | **Custom hvsocket RPC** | `vm.RPCServer` over `vm.HVSocketConn`/`HVSockAddr`/`HVSocketListener`; methods `Start`/`acceptLoop`/`handleConnection`/`handleMessage`/`handleResponse`/`handleEvent`/`SendGuestResponse` | **Control plane** to the in-guest `cowork-daemon`. Async, bidirectional request/response **+ events** over a single AF_HYPERV socket. Comes up first; bootstraps the rest. |
| 2 | **User-mode network over hvsocket** | `github.com/containers/gvisor-tap-vsock` (`pkg/tap` IP-pool/DHCP, `services/dns`/`dhcp`/`forwarder`, `virtualnetwork`, `types.ExposeRequest`/`UnexposeRequest`) + `inetaf/tcpproxy` | Guest has **no real Hyper-V NIC**; the host process *is* the guest's entire network in user space → DNS/DHCP/forward/**allowlist** all host-controlled. **This is the egress jail, by construction.** |
| 3 | **Exec/file plane** | Cowork symbols suggest SSH over the vsock network; Atelier instead uses guestd RPC for exec/`execInput` and Plan9/9p for files. | **Our implementation:** no sshd in the guest; guestd streams stdout/stderr as JSON-RPC notifications, and host folders mount over 9p. |

**The console pipe** `\\.\pipe\cowork-daemon-console-<vmid>` = the **guest serial console only** (`vm.ConsoleReader`/`readConsole`/`writeConsole`, bridged via `Microsoft/go-winio`). Boot log + daemon stdout/diagnostics — **not** control, **not** exec.

**Other verified facts:** file share = **Plan9/9p** (`vm.Plan9ShareInfo`), *not* virtiofs · HCS lifecycle via `vm.CreateComputeSystem`/`ModifyComputeSystem`/`ComputeSystemSummary` · initrd boot via `vm.SetInitrdPath` · named pipes via `Microsoft/go-winio`.

> **S6.1 update — concurrent per-session mounts + a host→guest input channel.** To run **many WORK sessions
> in ONE shared VM** (a VM-per-session would blow up host memory), the control plane gained two extensions:
> (1) **concurrent multi-share 9p mounts** — `attachWorkspace` takes a per-session `tag`/`port`/`target` (the
> broker tracks a `mounts` map and allocates vsock ports from a session pool above the default 564), so each
> session's host folder mounts at its own `/sessions/<id>` alongside the others (`ModifyComputeSystem` Add/
> Remove by tag); and (2) **`execInput`** — `exec` accepts a `sessionId` that registers the child's stdin in
> guestd; a new `execInput` RPC pushes bytes (a new user turn, or an `export_context`/`close` control) into
> that already-running process. Together these let each session run its **own persistent in-guest agent loop**
> (`cli-guest --serve`, NDJSON over stdio) that the host can feed, hibernate (export context → kill → detach),
> and resume (`query({resume})`).

> ✅ **Why Go — DECIDED (switched from Rust; this evidence is the reason).** The host stack is
> strongest in Go: `Microsoft/go-winio` covers named pipes and hvsockets,
> `containers/gvisor-tap-vsock` provides the no-NIC user-mode network, and hcsshim is the best
> public reference for HCS document shape. We still wrote our own thin `computecore.dll` bindings
> because hcsshim's reusable boot path is not usable for our non-GCS guest.

### Languages — two, by component (locked)
| Component | Language | Why |
|---|---|---|
| **Host service + broker** | **Go** | *Don't fight the ecosystem* — the reference stack is Go. **HCS** via our own thin `computecore.dll` bindings, **pipes + hvsocket** via **`Microsoft/go-winio`**, **user-mode net** via **`containers/gvisor-tap-vsock`** + `inetaf/tcpproxy`. New stack for the author — chosen deliberately. |
| **Agent loop / CLI (the brain)** | **TypeScript / Node** | Anthropic SDK is first-class in TS (streaming, tool-use, prompt caching). **Claude Code is itself Node**, and Cowork runs it inside the VM — so "TS agent in the guest" *is* the reference. |

> **Don't hand-write the loop — use `@anthropic-ai/claude-agent-sdk`** (verified: Cowork ships this exact public npm package, v0.3.142, as its agent loop). The SDK gives the tool-use loop, streaming, MCP wiring, and approval hooks out of the box. Our job is to **host** it and supply the seams (`executeTool` → guest, `callModel` → provider, approvals → broker), not to reimplement it. This shrinks M5 from "build an agent loop" to "wire the SDK's seams."

Write the agent loop as a **standalone Node module/CLI**, *not* welded to Electron internals, so the **same code** runs in Electron's main process (Topology A) and as a Node CLI in the guest (Topology B). **Host-Node → guest-Node = same runtime, no cross-compile.**

**HCS implementation update:** hcsshim remains the best reference for the compute-system document
shape, but the repo now uses its own thin `computecore.dll` bindings for lifecycle operations and
`Microsoft/go-winio` for named pipes and hvsockets. This avoids importing hcsshim `internal/`
packages and avoids its GCS-specific LCOW boot path.

### Agent topology: A → B (decided)
The agent loop's *location* is the one big runtime choice. Both topologies run the **same TS code**; only two seams differ — `executeTool` and the `callModel`/MCP transport.

- **Topology A — agent OUTSIDE.** Loop runs in Electron's main (Node); `executeTool` sends commands across Hop 3 into the VM. *Brain outside, hands inside (puppeteer).* Easy to build & debug.
- **Topology B — agent INSIDE.** Same loop runs as a Node CLI in the guest; its tools act directly
  on the guest filesystem through the SDK's built-in coding tools, and model calls leave through
  the host-enforced egress jail. Today the Anthropic key is still injected into the guest process;
  a host-side model proxy is tracked in [`vm-hardening.md`](vm-hardening.md).

**Decision: build A first, then migrate to B.** Not because A is throwaway — **the loop is reused** — but to **debug the agent and the hypervisor separately** instead of fighting both unknowns at once. Then merge two known-good halves.

---

## 9. Privilege / Elevation Model (verified)

- HCS compute-system creation **requires privilege** (Hyper-V Administrators or SYSTEM). A normal user cannot call HCS directly (verified: `hcsdiag` access denied).
- **Brokered model:** a SYSTEM service does the privileged work; the unprivileged app talks to it via a **named pipe gated by a security group**. (Docker: `com.docker.service` + `docker-users`. Cowork: `CoworkVMService` LocalSystem.)
- **Admin needed once, at install** (enable Virtual Machine Platform / install the service). **No per-run UAC.** Enterprise: IT does this via MSI/Intune — normal.

### Build implications
- **Dev (M0–M5):** add yourself to the **Hyper-V Administrators** group (`aka.ms/hcsadmin`) so the Go service (and `hcsdiag list`) work without full elevation each time — or run from an elevated terminal.
- **Ship (M6):** install the Go service as a **LocalSystem Windows service** + named pipe to Electron. End users then need **neither UAC nor Hyper-V-Admin membership** — identical to Cowork.

---

## 10. The Three Doors — detail

> **S6.1 update — no interactive approval (enterprise-fixed, user-proof, policy-guided).** The "explicit
> approval" language below described an earlier consent model. The shipped agent path has **no runtime
> approval and no override**: policy is pre-baked by the operator (and, in the product, distributed/updated
> centrally), then enforced + audited automatically. Allowed actions run and are audited; **denied actions
> don't run, warn the user, and are logged** (shown as display-only *policy-decision cards*). The goal is an
> agent that is USER-proof and policy-guided. Enforcement lives in the policy seam
> (`packages/agent/src/seams/policy.ts`, `mode:"guest"`) via the SDK `canUseTool` hook; the broker remains
> the wire-level gate/audit point. For Topology B (the in-guest loop) the cage boundary is the **VM**: in-cage
> file + shell tools are allowed; egress-bound tools (network) are denied.

### Files
- Read/list **auto-allowed** within the workspace.
- Writes / overwrites / deletes → audited by the **fixed policy** (no interactive approval; see the S6.1 note
  above). In-cage writes land in the sandbox; the *path jail* still applies for the host-side Files door.
- **Jail:** canonicalize every path against the workspace root; reject `..` and symlinks that escape.
- Every action → **audit log** (who/what/when/which file).

### Network
- Only via **connected MCP servers** (the egress allowlist = the set of configured servers). No raw sockets.
- Each tool call **policy-gated + audited**.
- Auth = corporate **OIDC** (already solved; out of scope for this project).

### Compute (Python)
- Real Python **inside the VM** (full ecosystem, real performance).
- VM network restricted (allowlist proxy, or **no-NIC + hvsocket-only broker**) → Python cannot exfiltrate; network is forced through the audited MCP layer.

---

## 11. UI / Frontend Stack (verified against Cowork)

**Layout decision: chat-forward** (Claude/Cowork model), not editor-forward (Codex). The conversation is center stage; files and tool runs render as **cards inside the stream**. Rationale: our users are **non-technical ops**, not developers — a Monaco-based IDE would intimidate. Monaco stays an *optional later* "view this file" affordance, never the centerpiece.

### Ground truth — what Claude Desktop / Cowork actually ships
Read directly from the installed app's `app.asar` `package.json` (`Claude_1.8089.1.0_x64`, Electron app, Vite-built — confirmed by `.vite/build` + `.vite/renderer` in the bundle). **This is source-of-truth, not a blog post.**

| Concern | Cowork's choice | Our decision |
|---|---|---|
| Shell | **Electron 41** | Electron (match) |
| Build / package | **Electron Forge 7.8 + `@electron-forge/plugin-vite`**; `maker-msix` (Windows), `maker-dmg`/`squirrel`/`pkg` | **Forge + Vite plugin**, `maker-msix` (we ship MSIX too). *(Revises earlier "electron-vite" call.)* |
| UI framework | **React 18.3** + `@vitejs/plugin-react` | React 18 (match) |
| Styling | **Tailwind 3.4** + `@tailwindcss/typography` (markdown prose) + `@tailwindcss/forms` | Tailwind + typography + forms (match) |
| Components | **roll-their-own on Tailwind** (no shadcn/Radix in deps) | **shadcn/ui** as accelerator (divergence — see §16 open Q) |
| Icons | **`@phosphor-icons/react`** | Phosphor (match) |
| Classnames | **clsx** | clsx |
| State | **RxJS 7** (no Redux/Zustand) | Zustand default; RxJS if needed (see §16) |
| i18n | **react-intl / FormatJS** | defer (single-locale to start) |
| Lint/format | **oxlint + oxfmt** (Rust-based Oxc) | adopt for the TS/UI side — fast (Go side uses `gofmt`/`go vet`) |
| Tests | **vitest** | vitest |

### Findings that reach beyond the UI (cross-cutting)
- **Agent loop = `@anthropic-ai/claude-agent-sdk`** (public npm). → See §8 / §14 M5; don't hand-write the loop.
- **`node-pty`** for shell exec/streaming → maps to our Hop-3 "exec into guest + stream stdout" (M2).
- **`@ant/cowork-win32-service`** = the `CoworkVMService` we found empirically → confirms the LocalSystem broker (§9).
- **`@ant/claude-ssh` + `ssh2` + `@ant/rfb-client` (VNC/RFB)** → Cowork reaches the guest over **SSH** and streams its **screen over VNC**. We likely skip GUI streaming (CSV/Excel/Python, not desktop pixels); SSH-to-guest is a candidate alternative to raw hvsocket exec.
- **`@modelcontextprotocol/sdk` + `@anthropic-ai/mcpb` + `@ant/dxt-registry`** → MCP everywhere; **DXT/`.mcpb`** is the skill-registry prior art (§12, §16).
- **`@ant/ipc-codegen`** → they **code-gen a typed IPC layer**. Strong pattern to copy for renderer ⇄ main ⇄ Go-sidecar.
- **electron-store** (settings), **winston** (logs), **`@sentry/electron`** (crash reporting), **https-proxy-agent** (egress).

### Multi-window architecture (observed)
Separate Vite renderer entries: `main_window`, `quick_window` (quick launcher), `buddy_window` (the cowork companion), `about_window`, `find_in_page`, plus a `computerUseTeach` onboarding view. → A desktop AI app is **several small windows**, not one monolith. Plan for it.

### The chat-stream guts (our build list)
- Markdown: a renderer + `@tailwindcss/typography` for prose styling.
- Code blocks: **Shiki** (VS Code grammars) for polish.
- Streaming: render tokens incrementally as they arrive over IPC; "stick to bottom unless the user scrolled up."
- Long threads: virtualize (`virtua` / `@tanstack/react-virtual`).
- Agent-specific: **tool-call cards** (collapsible "ran python" / "edited orders.csv"), **diff viewer** for file changes, **inline approval prompts** (the broker's gate), a **`/workspace` file panel** (list, not a full IDE tree).

### Recommended starting stack (our app)
**Electron Forge + Vite + React 18 + TypeScript + Tailwind (+typography/forms) + shadcn/ui + Phosphor icons + Zustand**, chat-forward layout, Shiki for code, **Monaco deferred**, oxlint/oxfmt + vitest for the toolchain.

---

## 12. Skills (distribution)

A **central registry** + client-side **install** ("plugins for ops users"). Two halves:
- **Distribution:** registry, versioning, **signing**, install/update.
- **Execution:** what an installed skill is *allowed* to do = the containment problem above (§2, §10).

Study Claude Desktop **Desktop Extensions** (`.dxt` / `.mcpb` one-click MCP bundles) as prior art for the install UX. Naming TBD.

---

## 13. Provider Seam & Data Residency

- **Provider seam:** for the experiment, hit the **Anthropic API** directly; design a thin provider abstraction so **Eliza** (Claude-API-shaped + corp auth/logging) can drop in later. Don't let provider-specifics leak past the seam.
- **Data residency caveat:** to reason about a file, its **contents are sent to the model**. With **Eliza (in-house datacenter)** data stays in the bank. With the **Anthropic experiment** it leaves the bank — **don't demo with real client data**.

---

# Part IV — Plan

## 14. Milestone Ladder (each is a real "it works" moment)

Historical milestone ladder. The main path is now implemented through S6.1, with live UI E2E,
service installation, pipe ACLs, and packaging still open. See
[`implementation-status.md`](implementation-status.md) for details.

- **M0 — Boot someone else's UVM.** Use hcsshim `uvmboot` / LCOW to boot a Linux utility VM and get a guest shell. Confirm Hyper-V + HCS work on the box. Read `internal/uvm`.
- **M1 — Drive HCS yourself.** Author the VM JSON compute-system doc, point at kernel + ext4 rootfs, `HcsCreateComputeSystem` + `Start` (via our `computecore.dll` bindings). *Your* VM boots.
- **M2 — Host↔guest bridge.** Over **hvsocket/vsock**: run a command in the guest, stream stdout back to the host.
- **M3 — Mount the workspace.** **Plan9/9p** share at `/workspace`, later generalized to per-session mounts at `/sessions/<id>`.
- **M4 — Lock egress.** No-NIC user-mode network over hvsocket with a broker-owned allowlist. Now it's a jail.
- **M5a — Agent loop on the HOST (Topology A).** Host **`@anthropic-ai/claude-agent-sdk`** in Electron's main (Node); wire its `executeTool` seam to `exec` into the guest over Hop 3. First working end-to-end agent on a real sandbox — wiring seams, not writing a loop.
- **M5b — Move the loop INTO the guest (Topology B).** Same SDK-hosted module runs as a Node CLI in the rootfs; tools act in the cage and model calls use the egress jail.
- **M6 — Electron shell.** UI ⇄ broker ⇄ guest; provider seam; fixed policy/audit cards; sidecar installed as a **LocalSystem service**.

> **M0–M2 alone** will teach you more about Cowork than almost anyone outside Anthropic.

---

# Back matter

## 15. References

**Microsoft / HCS**
- `microsoft/hcsshim` — the crown jewel (Go). Read `internal/uvm` (boots the Linux UVM), `internal/guest` (the **GCS** guest agent), `internal/hcs/system.go` (lifecycle), `internal/tools/uvmboot`, `vmcompute` bindings, `runhcs`.
- `microsoft/OpenGCS` — the Linux guest agent (now folded into hcsshim).
- `HcsCreateComputeSystem` — Microsoft Learn.
- Host Compute System Overview — Microsoft Learn.
- HCS Reference **Tutorial** — MicrosoftDocs/Virtualization-Documentation.
- `aka.ms/hcsadmin` — Hyper-V Administrators group.

**Open-source Go libs the host service is built from or mirrors**
- `microsoft/hcsshim` (MIT) — reference for HCS document shape and Plan9 conventions; not imported as the lifecycle driver.
- `Microsoft/go-winio` (MIT) — named pipes + hvsocket.
- `containers/gvisor-tap-vsock` (Apache-2.0) — user-mode network over vsock.
- `inetaf/tcpproxy` (Apache-2.0) — TCP forwarding.
- *(Anthropic's own `cowork-win32-service` is **closed** — 404 on GitHub; only the deps above are public.)*

**Cowork architecture / behavior**
- VentureBeat launch coverage.
- pvieito.com — "Inside Claude Cowork: How Anthropic Runs Claude Code in a Local VM."
- claudecn.com — Cowork architecture & security deep dives.
- blog.pluto.security — Cowork internals.
- Cowork-on-Windows + virtiofs/Plan9 bug threads: `anthropics/claude-code` #31520, #32172, #31991 (Hyper-V required).

**Privilege model**
- Docker Desktop — Windows permission requirements (privileged `com.docker.service` model).

---

## 16. Open Questions / Pending Decisions

- ~~**Rootfs distro**~~ → **DECIDED: Ubuntu 22.04 (glibc).** Mirrors Cowork; Python wheels just work. Alpine rejected (musl → wheel pain). See §7.
- ~~**Kernel**~~ → **DECIDED: generic Ubuntu kernel, matched to the Ubuntu userland** (keep kernel ↔ `/lib/modules` coupled). **Do NOT hand-compile.** M0 uses the tooling's **matched LCOW pair** as a throwaway bootstrap only. WSL2 kernel rejected (mismatch with userland). See §7.
- ~~**initramfs**~~ → **DECIDED: yes — a matching boot initramfs** (the generic Ubuntu kernel ships drivers as modules, so it's required; confirmed by the on-disk `initrd`). Built with `mkinitramfs` against the kernel version; ship `/lib/modules/<ver>` in the rootfs. See §7. **Verified (S0a, 2026-05-20):** unpacked Cowork's `initrd` — it's a textbook `initramfs-tools` boot initramfs (stock `/init`, `scripts/`, `conf/`, `cryptroot/`; ~482 MB once decompressed = modules+firmware), whose only job is to mount `rootfs.vhdx` and pivot. Exactly our S1.3 model.
- ~~**HCS access strategy** (own bindings vs vendor hcsshim `internal/` vs shell-out to `uvmboot`)~~ → **DECIDED and implemented:** roll our own thin `computecore.dll` bindings + author our own compute-system JSON doc. hcsshim remains the reference for document shape, but its `internal/` packages are not importable and its LCOW path is welded to Microsoft's GCS guest. See `implementation-status.md` S0a → S1.2.
- ~~**File share:** virtiofs vs Plan9/9p~~ → **RESOLVED (lean Plan9/9p).** Cowork uses **Plan9** (`vm.Plan9ShareInfo`), not virtiofs — matches the Windows virtiofs bug threads. See §8 Hop 3.
- ~~**Egress design**~~ → **DECIDED and implemented:** no-NIC user-mode network over hvsocket using `containers/gvisor-tap-vsock`, with a broker-owned default-deny hostname allowlist and DNS pinning. See `implementation-status.md` S4.1.
- **Skill registry:** naming + bundle format. (Cowork's analog = **DXT / `.mcpb`** desktop-extension bundles — strong prior art; see §11.)
- ~~**App rename** away from "theparser."~~ → **DECIDED: Atelier.**
- **Component library:** **shadcn/ui (Radix)** as an accelerator *vs* roll-your-own on Tailwind (what Cowork does). Lean shadcn for speed; revisit if the look diverges. See §11.
- **Renderer state:** **Zustand** (simple) *vs* **RxJS** (what Cowork uses; better for token/event streams, steeper curve). Default Zustand; reach for RxJS only if streaming state gets gnarly. See §11.

---

## 17. Glossary / Keyword Dictionary

Quick decoder for the jargon in this doc. Grouped by area; one line each.

### Windows virtualization
- **Hyper-V** — Microsoft's type-1 hypervisor; the foundation under all the surfaces below.
- **HCS (Host Compute System)** — the low-level API (`vmcompute.exe` / `computecore.dll`, `HcsCreateComputeSystem`) for creating/managing VMs & containers. Used by WSL2, Docker, Windows Sandbox, Cowork. VMs created here are **invisible to Hyper-V Manager**.
- **VMMS** — the classic Hyper-V management service (`vmms.exe`) behind Hyper-V Manager; the surface we are **not** using.
- **WHP (Windows Hypervisor Platform)** — public API (`WinHvPlatform.dll`, `WHvCreatePartition`) for 3rd-party hypervisors (VirtualBox, QEMU). Not used here.
- **vmcompute** — the HCS service/process that actually does the work; runs as SYSTEM.
- **vmwp.exe** — VM Worker Process; one per running VM, hosts that VM's virtual devices.
- **hcsshim** — Microsoft's open-source **Go** library wrapping HCS; our primary reference for HCS
  document shape and Plan9 conventions, not our lifecycle driver.
- **LCOW (Linux Containers on Windows)** — running Linux under HCS by direct-booting a kernel + rootfs (no full installer). Our boot model.
- **UVM (Utility VM)** — a lightweight, purpose-built VM (like Cowork's) — not a general-purpose desktop VM.
- **GCS (Guest Compute Service) / OpenGCS** — Microsoft's in-guest Linux agent that talks to HCS. Cowork ships its *own* daemon instead — **confirmed (S0a, 2026-05-20): `coworkd`**, a Go binary on `smol-bin.vhdx` (no GCS/`vsockexec` present), talking over `hv_sock`, with built-in egress control (`cowork-egress-blocked`) and an inner `bwrap` sandbox. This forces our **own-bindings** HCS path (§16) and is the model for our `guestd` (§8 Hop 3).
- **KernelDirect** — booting by handing the hypervisor a kernel file directly, skipping BIOS/GRUB.
- **VHD / VHDX** — Microsoft virtual hard-disk file formats; our rootfs/session disks are VHDX.
- **VMBus** — the Hyper-V guest↔host device channel; drivers like `hv_storvsc` (disk), `hv_netvsc` (net), `hv_sock` (sockets) ride it.
- **Hyper-V Administrators** — the Windows security group that may call HCS without full admin (`aka.ms/hcsadmin`).
- **WSL2** — Microsoft's Linux subsystem; also HCS-based, but its `-microsoft-standard-WSL2` kernel is **not** what Cowork uses.

### Host ↔ guest plumbing
- **hvsocket** — Hyper-V socket on the host side (`AF_HYPERV`); a stream channel between host and guest with no NIC. The backbone of Hop 3.
- **vsock** — the guest-side counterpart (`AF_VSOCK`, addressed by CID/port); pairs with hvsocket.
- **named pipe** (`\\.\pipe\name`) — Windows local IPC channel; our Hop 2 (app ⇄ service) and Cowork's `cowork-vm-service` / console pipes use these.
- **9p / Plan9** (`9pnet_virtio`) — a network filesystem protocol used to share a host folder into the VM; **what Cowork uses** for `/workspace`.
- **virtiofs** — a faster FUSE-based host↔guest share; the alternative to 9p (flaky on Windows builds, hence Cowork's 9p choice).
- **virtio** — the standard paravirtualized device family (blk/scsi/net/fs) for VMs.
- **gvisor-tap-vsock** — Go user-mode TCP/IP stack (DHCP/DNS/forwarding) that gives a guest networking **over vsock with no real NIC**; the host becomes the whole network → built-in egress control.
- **tcpproxy** (`inetaf/tcpproxy`) — Go library for rule-based TCP forwarding; pairs with the above.
- **HNS / HCN** — Host Networking Service / Host Compute Network; Windows' built-in VM networking (the "real NIC + proxy" alternative to gvisor).
- **serial console** (`hvc0`) — the VM's text console; Cowork bridges it to the `cowork-daemon-console-<vmid>` named pipe for logs.
- **SSH / sshd / sftp** — Cowork's exec/file plane: the host SSHes into an sshd in the guest *over the vsock network* to run commands and move files.

### Linux guest
- **kernel** (`vmlinux` / `vmlinuz` / `bzImage`) — the OS core, direct-booted by the host.
- **rootfs** — the userland filesystem ("the distro"): libraries, Python, tools, init.
- **initramfs / initrd** — a tiny in-RAM filesystem loaded before the real root, to bring up storage/transport drivers that are built as modules.
- **ext4** — the Linux filesystem we put the rootfs in (inside a VHDX).
- **overlayfs / tmpfs** — layered/in-RAM filesystems; used for the per-session ephemeral layer over a read-only base.
- **glibc vs musl** — the two C libraries; **glibc** (Ubuntu/Debian) makes Python wheels "just install"; **musl** (Alpine) often forces source compiles → rejected.
- **PID 1 / init** — the first process the kernel runs; the root of the process tree (busybox `sh` to start, our daemon later).
- **/lib/modules, modprobe, DKMS** — the kernel-module ecosystem; only works when the running kernel **matches** the userland version (the "coupling" rule).
- **syscall ABI** — the stable kernel↔userland call interface; why any userland *runs* on any modern kernel even when modules don't match.
- **bubblewrap (bwrap)** — an unprivileged sandbox tool (filesystem view, namespaces, dropped capabilities); Cowork's *in-VM* second layer.
- **seccomp** — Linux syscall filtering; restricts what even sandboxed processes may call.
- **namespaces / capabilities** — kernel isolation primitives bwrap builds on (separate views of FS/net/PID; fine-grained privilege bits).
- **pip / wheels** — Python's installer and its prebuilt binary packages (the glibc/musl pain point).

### Electron / desktop app
- **Electron** — Chromium + Node.js desktop-app framework; our shell.
- **main process / renderer** — Electron's privileged Node side (`main`) vs the sandboxed UI (`renderer`).
- **preload** — the small bridge script exposing a **narrow, allowlisted** API from main to renderer.
- **IPC (ipcRenderer / ipcMain)** — Electron's built-in renderer↔main messaging (our Hop 1).
- **contextIsolation / sandbox / nodeIntegration / CSP** — the renderer hardening switches (first three on/off as shown; CSP = Content-Security-Policy).
- **Electron Forge** — the build/package/publish toolchain Cowork (and we) use; with the **Vite** plugin and `maker-msix`.
- **Vite** — the fast bundler/dev-server under Forge.
- **asar** — Electron's app-archive format (we read Cowork's `app.asar` to learn its stack).
- **MSIX** — modern Windows app-package/installer format (how Claude/Cowork ships).
- **N-API / FFI** — Node's native-addon ABI / foreign-function interface; the *in-process* native-code path we **rejected** (Option 1). (Rust's `napi-rs` and Go's `cgo c-shared` are the language-specific variants.)
- **node-pty** — pseudo-terminal library for running & streaming a shell from Node; maps to our guest-exec channel.

### AI / agent
- **agent loop** — the model↔tools cycle (call model → run tool → feed result → repeat); we host the SDK's, not hand-write it.
- **Claude Agent SDK** (`@anthropic-ai/claude-agent-sdk`) — Anthropic's public npm agent loop; Cowork ships it, so do we.
- **tool-use** — the model calling structured "tools" (functions) we expose, gated by the broker.
- **MCP (Model Context Protocol)** — the open standard for connecting models to tools/data; our network door = connected MCP servers.
- **DXT / `.mcpb`** — Desktop Extensions: one-click MCP-server bundles; prior art for our skills registry.
- **Cowork** — Anthropic's consumer "Claude Code for non-coders" desktop agent; our reference architecture.
- **Eliza** — BNY's in-house, Claude-API-shaped AI stack; the eventual provider behind the seam.
- **provider seam** — the thin abstraction that lets us swap Anthropic API ↔ Eliza without leaking provider specifics.
- **prompt injection** — malicious instructions hidden in content the model reads; the core reason for containment.
- **ambient authority** — implicit power a process has just by running; containment = **removing** it (the agent acts only through gated capabilities).
- **RFB / VNC** — remote-framebuffer screen streaming; how Cowork shows the VM's GUI (we likely skip it).

### Protocol & glue
- **JSON-RPC 2.0** — the request/response/notification wire format on the broker pipe.
- **notification (vs request)** — a JSON-RPC message with **no `id`**; used for one-way streaming (stdout/progress/logs).
- **Content-Length framing** — LSP/DAP-style message delimiting by byte-length header (robust vs newline-delimited).
- **OpenTelemetry (OTel)** — tracing/metrics standard; Cowork instruments every RPC with it.
- **Zod / serde / encoding/json** — schema-validation/serialization (TS / Rust / Go respectively).
- **OIDC** — OpenID Connect; the corporate user-auth (BNY-provided, out of scope here).
- **Topology A / B** — agent loop **outside** the VM (host) vs **inside** the VM; we do A then B.
- **broker** — the policy/approval/audit gate in the privileged service; *the* containment chokepoint.
- **egress / allowlist** — outbound network control; only approved destinations (the network jail).
- **Pyodide / WASM** — Python-compiled-to-WebAssembly (in-app); the low-isolation compute option we **rejected**.
