#!/usr/bin/env python3
"""Compile an OCI/Docker seccomp profile (JSON) into a cBPF program for `bwrap --seccomp`.

The profile is Docker's default, vendored from moby/profiles (see SOURCE). We evaluate it
for a NO-CAPABILITY process, because the agent runs under `bwrap --cap-drop ALL`. That single
fact is what makes the profile deny unshare()/setns()/mount()/clone3() and restrict clone()
to non-namespace flags — closing docs/security.md F-01 (and F-13) with no custom rule:
CAP_SYS_ADMIN-gated entries simply don't apply, so they fall through to the default ERRNO,
while the no-cap clone rule allows thread creation but not CLONE_NEW* namespaces.

Usage: compile-seccomp.py <profile.json> <out.bpf>

The blob is exported for the *native* architecture of the machine running this script, so it
MUST run in a target-arch container (image/build.sh uses --platform linux/{amd64,arm64}).
"""
import json
import os
import platform
import sys

import seccomp

# bwrap drops ALL capabilities, so model the agent as having none.
PROCESS_CAPS = frozenset()

# uname machine -> OCI arch token used in the profile's includes/excludes.arches.
MACHINE_TO_OCI = {"x86_64": "amd64", "aarch64": "arm64"}
# uname machine -> native SCMP_ARCH_* token (to locate subarchitectures in archMap).
MACHINE_TO_SCMP = {"x86_64": "SCMP_ARCH_X86_64", "aarch64": "SCMP_ARCH_AARCH64"}
# SCMP_ARCH_* token -> seccomp.Arch attribute name.
SCMP_TO_ARCH = {
    "SCMP_ARCH_X86": "X86",
    "SCMP_ARCH_X86_64": "X86_64",
    "SCMP_ARCH_X32": "X32",
    "SCMP_ARCH_ARM": "ARM",
    "SCMP_ARCH_AARCH64": "AARCH64",
}
OP = {
    "SCMP_CMP_NE": seccomp.NE,
    "SCMP_CMP_LT": seccomp.LT,
    "SCMP_CMP_LE": seccomp.LE,
    "SCMP_CMP_EQ": seccomp.EQ,
    "SCMP_CMP_GE": seccomp.GE,
    "SCMP_CMP_GT": seccomp.GT,
    "SCMP_CMP_MASKED_EQ": seccomp.MASKED_EQ,
}


def to_action(name, errno_ret, default_errno):
    if name == "SCMP_ACT_ALLOW":
        return seccomp.ALLOW
    if name == "SCMP_ACT_ERRNO":
        return seccomp.ERRNO(errno_ret if errno_ret is not None else default_errno)
    if name == "SCMP_ACT_LOG":
        return seccomp.LOG
    if name in ("SCMP_ACT_KILL", "SCMP_ACT_KILL_THREAD"):
        return seccomp.KILL
    if name == "SCMP_ACT_KILL_PROCESS":
        return seccomp.KILL_PROCESS
    raise ValueError("unsupported action: " + name)


def applies(entry, arch_token):
    """Mirror the OCI includes/excludes match for our (no-cap, single-arch) process."""
    inc = entry.get("includes", {})
    exc = entry.get("excludes", {})
    # includes: the rule applies only if the process satisfies ALL of them.
    if inc.get("caps") and not set(inc["caps"]).issubset(PROCESS_CAPS):
        return False
    if inc.get("arches") and arch_token not in inc["arches"]:
        return False
    # minKernel ignored: the guest kernel is modern; treat as satisfied.
    # excludes: the rule is dropped if the process matches ANY of them.
    if exc.get("caps") and set(exc["caps"]) & PROCESS_CAPS:
        return False
    if exc.get("arches") and arch_token in exc["arches"]:
        return False
    return True


def build_args(args):
    out = []
    for a in args:
        op = OP[a["op"]]
        if op == seccomp.MASKED_EQ:
            out.append(seccomp.Arg(a["index"], op, a["value"], a.get("valueTwo", 0)))
        else:
            out.append(seccomp.Arg(a["index"], op, a["value"]))
    return out


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: compile-seccomp.py <profile.json> <out.bpf>")
    profile_path, out_path = sys.argv[1], sys.argv[2]

    machine = platform.machine()
    if machine not in MACHINE_TO_OCI:
        sys.exit("unsupported build arch: " + machine)
    arch_token = MACHINE_TO_OCI[machine]

    with open(profile_path) as fh:
        prof = json.load(fh)
    default_errno = prof.get("defaultErrnoRet", 1)

    flt = seccomp.SyscallFilter(to_action(prof["defaultAction"], None, default_errno))

    # The native arch is auto-added; add its 32-bit/compat subarchitectures too (matching
    # Docker) so compat syscalls are filtered rather than killed as a foreign architecture.
    for am in prof.get("archMap", []):
        if am.get("architecture") != MACHINE_TO_SCMP[machine]:
            continue
        for sub in am.get("subArchitectures", []):
            attr = SCMP_TO_ARCH.get(sub)
            if not attr:
                continue
            try:
                flt.add_arch(getattr(seccomp.Arch, attr))
            except Exception as e:  # noqa: BLE001 - best-effort, keep going
                print("warn: add_arch %s: %s" % (sub, e), file=sys.stderr)

    added = skipped = 0
    for entry in prof["syscalls"]:
        if not applies(entry, arch_token):
            continue
        act = to_action(entry["action"], entry.get("errnoRet"), default_errno)
        args = build_args(entry.get("args", []))
        for name in entry["names"]:
            try:
                flt.add_rule(act, name, *args)
                added += 1
            except Exception as e:  # noqa: BLE001 - unknown syscall or redundant rule
                print("warn: skip %s: %s" % (name, e), file=sys.stderr)
                skipped += 1

    with open(out_path, "wb") as out:
        flt.export_bpf(out)
    print(
        "seccomp: arch=%s rules_added=%d skipped=%d -> %s (%d bytes)"
        % (arch_token, added, skipped, out_path, os.path.getsize(out_path)),
        file=sys.stderr,
    )


if __name__ == "__main__":
    main()
