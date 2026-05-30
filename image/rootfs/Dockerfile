# Rootfs source for the utility VM (design.md §7). We don't run this as a
# container — `image/build.sh rootfs` does `docker export` of it and pours the
# result into an ext4 VHD. glibc (Ubuntu) is deliberate: Python wheels just work.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# Manifest (design.md §7): init shell, python3+pip, git, ripgrep, CA certs.
# Node is installed from NodeSource below (not apt — Ubuntu 22.04's `nodejs` is
# v12, far too old for the agent SDK). The guest bridge daemon (runner, design.md
# §8 Hop 3) is cross-compiled and injected by image/build.sh (not apt).
#
# The matched guest kernel (design.md §7 coupling rule) is installed here too, so
# the kernel, its /lib/modules/<ver>, and the boot initramfs all come from ONE apt
# transaction — they can never drift. linux-image-virtual-hwe-24.04 is the rolling
# HWE kernel trimmed for VM guests: the same generic kernel binary, but it pulls
# only the base linux-modules set (NOT linux-modules-extra) to slim /lib/modules.
# We rely on that base set carrying every driver this VM needs (hv_*, virtio incl.
# virtio-vsock + virtiofs, 9p, ext4/overlay/fuse) — proven at boot by the modprobe
# + virtiofs checks in image/guest/init.sh. initramfs-tools is explicit (the kernel
# meta only recommends it, and --no-install-recommends would drop it) so the kernel
# postinst generates /boot/initrd.img-<ver>. image/build.sh extracts vmlinuz +
# initrd from /boot.
# Networking is static (CRIT-01/05 hardening): runner configures tap0 itself
# (192.168.127.2/24, gateway .1) and runs gvforwarder with -preexisting, so no DHCP
# client is needed. iproute2 brings the interface up (and inspects egress); curl
# exercises it. bubblewrap (bwrap) sandboxes the in-guest agent as a non-root user
# (CRIT-01). openssh-server is intentionally NOT installed (no inbound service runs;
# it only left world-readable host keys behind). gnupg + curl are also what the
# NodeSource setup script needs to add its apt repo. tmux backs partisan's OpenHands
# TerminalTool (libtmux) — its venv ships on the runner volume but needs a tmux binary here.
RUN apt-get update && apt-get install -y --no-install-recommends \
      busybox-static \
      python3 \
      python3-pip \
      tmux \
      git \
      ripgrep \
      ca-certificates \
      gnupg \
      bubblewrap \
      iproute2 \
      curl \
      initramfs-tools \
      linux-image-virtual-hwe-24.04 \
    && rm -rf /var/lib/apt/lists/*

# Modern Node + npm (NodeSource). The in-guest agent loop (Topology B, S5b.1) runs via
# tsx under this Node runtime; the SDK requires Node >=18, pin the 22.x line to match
# packages/artisan's engines. (The agent's own code + node_modules are NOT baked here — they
# ship on the runner volume; see /opt below and image/agent/Dockerfile.)
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# /workspace is the only persistent mount (9p/virtiofs share from the host).
RUN mkdir -p /workspace

# /opt is the mountpoint for the runner volume (LABEL=runner), which carries BOTH runner
# (/opt/runner/atelier-runner) and the in-guest agent (/opt/atelier) — neither is baked into the
# rootfs, so both iterate in seconds without a rootfs rebuild (image/build.sh runner,
# image/guest/init.sh). It exists in the base image; the mount shadows it at boot.

# Non-root agent user (CRIT-01): the in-guest agent drops to uid 1001 via bwrap. The agent
# tree under /opt is root-owned and read-only at runtime (the agent only reads/execs it); its
# writable HOME is /home/atelier (a tmpfs at boot — see image/guest/init.sh). /sessions is the
# parent of per-session 9p mount points (also a tmpfs at boot), so runner can mkdir under it on
# the read-only root.
# Login shell is /bin/bash, not nologin: partisan's OpenHands TerminalTool runs tmux, whose
# default-shell is derived from this passwd entry — nologin makes every pane exit 1 (no shell),
# so the agent's terminal turns fail. No login service runs (openssh is intentionally absent),
# so nologin bought no hardening here.
RUN useradd -u 1001 -m -d /home/atelier -s /bin/bash atelier \
    && mkdir -p /sessions

# init + guest config are installed by image/build.sh from image/guest/.
# Sensitive permissions (/etc/shadow, etc.) and the static /etc/resolv.conf are
# normalized by the imager step in image/build.sh, which also builds the ext4 inside
# Linux so file ownership/modes are preserved (the CRIT-05 fix).
CMD ["/bin/bash"]
