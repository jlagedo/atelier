# Security Findings — VM Sandbox

**Date:** 2026-05-24  
**Found by:** Claude (sandboxed agent, session sb9bf63633e)

---

## 🔴 Issue: `guestd` mount visible inside bwrap sandbox

### What was found
The agent inside the bwrap sandbox can see the `guestd` volume mounted at `/opt/guestd`:

```
/dev/vdb /opt/guestd ext4 ro,nosuid,nodev,relatime 0 0
```

Running `ls /opt/guestd/` from inside the sandbox returns:
```
guestd
lost+found
```

The agent should have **zero visibility** into the host communication layer.

---

### Root Cause

The bwrap command uses `--bind / /` which binds the **entire root filesystem** into the sandbox, making every mount — including `/opt/guestd` — visible to the agent.

```bash
# Current (problematic)
bwrap --bind / / ...
# /opt/guestd is fully visible inside the sandbox
```

---

### Fix

Add `--tmpfs /opt/guestd` **after** `--bind / /` in the bwrap invocation. bwrap processes bind arguments in order, so a later entry shadows the earlier one for that specific path.

```bash
# Fixed
bwrap \
  --bind / / \
  --tmpfs /opt/guestd \   # shadows the real mount with an empty tmpfs
  ...
```

Optionally, also hide the agent runtime directory:
```bash
bwrap \
  --bind / / \
  --tmpfs /opt/guestd \
  --tmpfs /opt/atelier \  # hides agent runtime internals too
  ...
```

---

### Impact

- Agent can enumerate host communication infrastructure
- Agent can see the `guestd` binary path and filename
- Reveals implementation details of the VM orchestration layer

---

### Status
- [ ] Fix bwrap invocation to shadow `/opt/guestd`
- [ ] Consider shadowing `/opt/atelier` as well
- [ ] Verify fix by re-running `cat /proc/mounts` from inside a new session
