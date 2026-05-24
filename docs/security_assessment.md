# Complete Security Assessment — VM Sandbox

**Date:** 2026-05-24  
**Assessor:** Claude (sandboxed agent, session sb9bf63633e)  
**Environment:** Ubuntu 24.04.4 LTS · Linux 6.17.0 · aarch64 · Apple Silicon VZ VM

---

## Summary

| Severity | Count |
|---|---|
| 🔴 Critical | 1 |
| 🟠 High | 3 |
| 🟡 Medium | 3 |
| 🟢 Info / Good | 7 |

---

## 🔴 Critical

### 1. Unprivileged User Namespace Creation Grants Full Capabilities

**Command:**
```bash
unshare --user --map-root-user id
# → uid=0(root) gid=0(root) groups=0(root)

unshare --user --map-root-user cat /proc/self/status | grep Cap
# CapPrm: 000001ffffffffff  ← ALL capabilities
# CapEff: 000001ffffffffff  ← ALL capabilities effective
# CapBnd: 000001ffffffffff  ← ALL capabilities in bounding set
```

**What this means:**  
Any process inside the sandbox can create a new user namespace and gain all Linux capabilities *within that namespace* as apparent root. With no seccomp filter in place, this is a meaningful attack surface:

- Can be used to exploit kernel vulnerabilities that require `CAP_SYS_ADMIN` or similar
- With `CAP_NET_ADMIN` inside a user namespace + new network namespace, can manipulate network stack
- Historically, unprivileged user namespaces are the #1 source of kernel privilege escalation CVEs on Linux (e.g. Dirty Pipe, various overlayfs escapes)

**Why it's not an instant escape:**  
The new user namespace is still subject to the outer kernel's security boundary. Capabilities are scoped to the namespace. But combined with no seccomp and a kernel that may have vulnerabilities, it is a viable attack chain.

**Fix:**
```bash
# Option A: Disable unprivileged user namespaces entirely
echo 0 > /proc/sys/kernel/unprivileged_userns_clone
# or in sysctl.conf:
kernel.unprivileged_userns_clone = 0

# Option B: Restrict via seccomp (also fixes the seccomp gap — see Medium section)
# Block: CLONE_NEWUSER flag in clone()/unshare() syscalls via seccomp profile
```

---

## 🟠 High

### 2. `guestd` Binary and Mount Visible Inside Sandbox

Already documented in `security_findings.md`. Root cause: `--bind / /` in bwrap exposes all mounts.

Additionally confirmed: the `guestd` ELF binary (4 MB, Go binary) is fully readable from inside the sandbox, including via a user namespace. An attacker could read and analyze it to understand the host communication protocol.

**Fix:** Add `--tmpfs /opt/guestd` after `--bind / /` in bwrap invocation.

---

### 3. Agent Runtime Source Code Fully Readable

The entire `/opt/atelier` tree is readable from inside the sandbox:

```
/opt/atelier/packages/agent/src/seams/policy.ts     ← full policy engine logic
/opt/atelier/packages/agent/src/broker/client.ts     ← broker client
/opt/atelier/packages/agent/src/cli-guest.ts         ← guest entrypoint
/opt/atelier/packages/protocol/schema/protocol.json  ← full RPC protocol schema
/opt/atelier/packages/provider/src/index.ts          ← provider source
```

**What this exposes:**
- Complete policy evaluation logic (GUEST_ALLOW/GUEST_DENY sets, door classification)
- Full broker RPC protocol: `createVM`, `startVM`, `stopVM`, `exec`, `execInput`, `attachWorkspace`, `detachWorkspace`, `readFile`, `writeFile`, `setEgressPolicy`
- The `setEgressPolicy` RPC is particularly interesting — if reachable, it could be used to whitelist network egress
- Implementation details that aid targeted exploitation

**Fix:** Add `--tmpfs /opt/atelier` after `--bind / /` in bwrap invocation (same pattern as guestd fix).

---

### 4. `kptr_restrict = 0` — Kernel Pointer Restriction Disabled

```bash
cat /proc/sys/kernel/kptr_restrict  # → 0
```

With value `0`, kernel pointers in `/proc/kallsyms` and other interfaces are not restricted. In practice, `/proc/kallsyms` is returning all-zero addresses (likely due to kernel ASLR + user namespace mapping), but the setting itself is wrong and may leak pointers through other interfaces (`/proc/net`, dmesg, etc.).

**Fix:**
```bash
# sysctl.conf:
kernel.kptr_restrict = 2   # hide from all users including root
```

---

## 🟡 Medium

### 5. No Seccomp Filter

```
Seccomp: 0
Seccomp_filters: 0
```

No syscall filtering is applied. While all capabilities are dropped, the seccomp gap means:
- The full Linux syscall surface is available
- Combined with user namespace capability escalation (#1), this significantly widens the attack surface for kernel exploits

**Fix:** Apply a seccomp profile to the bwrap invocation:
```bash
bwrap --seccomp <fd> ...
# Use a profile derived from Docker's default seccomp policy, 
# additionally blocking: unshare, clone (CLONE_NEWUSER), keyctl, 
# perf_event_open, bpf, userfaultfd
```

---

### 6. Speculation Store Bypass Vulnerable

```
Speculation_Store_Bypass: vulnerable
SpeculationIndirectBranch: unknown
```

The VM is not applying speculative execution mitigations (SSBD). On Apple Silicon this is lower risk than x86, but worth noting for multi-tenant scenarios.

**Fix:** Add `spec_store_bypass_disable=seccomp` or `spec_store_bypass_disable=on` to kernel cmdline. Currently kernel is booted with: `console=hvc0 root=/dev/vda ro noresume init=/sbin/init` — no mitigations specified.

---

### 7. Sensitive Data in Readable Home Directory

```
/home/atelier/.claude.json  → contains userID hash
/home/atelier/.claude/policy-limits.json  → reveals restriction config
```

The `userID` field (`b1e87572...`) and full policy-limits configuration are readable. While not immediately exploitable, they reveal operational details.

**Fix:** These files are owned by `atelier` (the agent user) so the agent needs to read them. Consider moving policy-limits out of the agent-readable path or encrypting sensitive fields.

---

## 🟢 Good — What's Working Well

| Control | Status |
|---|---|
| Running as unprivileged user (uid=1001) | ✅ |
| All capabilities dropped on main process (`CapEff: 0`) | ✅ |
| `NoNewPrivs = 1` on main process | ✅ |
| No listening network ports | ✅ |
| Root filesystem mounted read-only (`ro`) | ✅ |
| User/PID/IPC/UTS/MNT namespace isolation via bwrap | ✅ |
| `sudo` not installed | ✅ |
| No SUID binaries found | ✅ |
| `dmesg_restrict = 1` | ✅ |
| `perf_event_paranoid = 4` (most restrictive) | ✅ |
| `unprivileged_bpf_disabled = 2` (BPF restricted) | ✅ |
| `ptrace_scope = 1` (restricted ptrace) | ✅ |
| No `/dev/vsock` exposed in sandbox | ✅ |
| WebFetch / WebSearch denied by policy engine | ✅ |
| Mounts are `nosuid,nodev` | ✅ |

---

## Recommended Fix Priority

```
1. [CRITICAL] Add seccomp profile + block CLONE_NEWUSER         → closes user ns escalation
2. [HIGH]     --tmpfs /opt/guestd in bwrap                      → hides host comm layer
3. [HIGH]     --tmpfs /opt/atelier in bwrap                     → hides runtime source
4. [HIGH]     kernel.kptr_restrict = 2 in sysctl               → hides kernel pointers
5. [MEDIUM]   Add seccomp to bwrap (--seccomp fd)               → reduces syscall surface
6. [MEDIUM]   Add spec_store_bypass_disable to kernel cmdline   → spectre/ssb mitigation
```

---

## Architecture Notes

The vsock transport (`vmw_vsock_virtio_transport`) is loaded and active — this is the guestd ↔ host communication channel. The broker RPC protocol (protocol.json) exposes powerful operations including `setEgressPolicy` and `attachWorkspace`. Ensuring the vsock channel is not reachable from within the bwrap sandbox (no `/dev/vsock` exposed — ✅ already the case) is important to maintain.

The policy engine (`policy.ts`) correctly denies `WebFetch`/`WebSearch` and defaults to deny for unknown tools. The `AllowAll` comment in the source (`"the broker's server-side gate remains AllowAll today"`) suggests the host-side enforcement is not yet fully hardened — worth revisiting.
