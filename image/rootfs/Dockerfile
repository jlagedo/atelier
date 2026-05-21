# Rootfs source for the utility VM (design.md §7). We don't run this as a
# container — `image/build.sh rootfs` does `docker export` of it and pours the
# result into an ext4 VHD. glibc (Ubuntu) is deliberate: Python wheels just work.
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# Manifest (design.md §7): init shell, python3+pip, git, ripgrep, CA certs.
# Node is installed from NodeSource below (not apt — Ubuntu 22.04's `nodejs` is
# v12, far too old for the agent SDK). The guest bridge daemon (guestd, design.md
# §8 Hop 3) is cross-compiled and injected by image/build.sh (not apt).
#
# The matched guest kernel (design.md §7 coupling rule) is installed here too, so
# the kernel, its /lib/modules/<ver>, and the boot initramfs all come from ONE apt
# transaction — they can never drift. linux-image-generic-hwe-22.04 mirrors
# Cowork's 6.8.0-*-generic and pulls linux-modules{,-extra} (hv_*, virtio, 9p,
# ext4/overlay/fuse). initramfs-tools is explicit (the kernel meta only recommends
# it, and --no-install-recommends would drop it) so the kernel postinst generates
# /boot/initrd.img-<ver>. image/build.sh extracts vmlinuz + initrd from /boot.
# isc-dhcp-client (dhclient): the guest network forwarder (gvforwarder, S4.1) runs
# it on the tap interface to take a DHCP lease from the host's user-mode network
# (gvisor-tap-vsock) — it configures the address, default route, and resolv.conf.
# iproute2 + curl are for exercising/inspecting egress from the guest. gnupg + curl
# are also what the NodeSource setup script needs to add its apt repo.
RUN apt-get update && apt-get install -y --no-install-recommends \
      busybox-static \
      python3 \
      python3-pip \
      git \
      ripgrep \
      ca-certificates \
      gnupg \
      openssh-server \
      isc-dhcp-client \
      iproute2 \
      curl \
      initramfs-tools \
      linux-image-generic-hwe-22.04 \
    && rm -rf /var/lib/apt/lists/*

# Modern Node + npm (NodeSource). The in-guest agent loop (Topology B, S5b.1) and
# the SDK require Node >=18; pin the 22.x line to match packages/agent's engines.
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# /workspace is the only persistent mount (9p/virtiofs share from the host).
RUN mkdir -p /workspace

# In-guest agent loop (Topology B, S5b.1, design.md §8): the same TS loop as the
# host (Topology A) but running in the cage, using the SDK's built-in tools on the
# guest fs. image/build.sh stages packages/{agent,provider,protocol} into the build
# context (source only); we install the agent's runtime deps here so they're baked
# into the rootfs (linux/amd64, correct platform binaries). Run via tsx; the agent
# imports provider/protocol by relative path, so the layout must be preserved.
COPY packages /opt/atelier/packages
RUN cd /opt/atelier/packages/agent \
    && npm ci --omit=dev \
    && npm cache clean --force

# init + guest config are installed by image/build.sh from image/guest/.
CMD ["/bin/bash"]
