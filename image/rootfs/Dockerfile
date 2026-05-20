# Rootfs source for the utility VM (design.md §7). We don't run this as a
# container — `image/build.sh rootfs` does `docker export` of it and pours the
# result into an ext4 VHD. glibc (Ubuntu) is deliberate: Python wheels just work.
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# Manifest (design.md §7): init shell, python3+pip, node, git, ripgrep, CA certs.
# The guest bridge daemon + TS agent CLI are layered in later milestones.
#
# The matched guest kernel (design.md §7 coupling rule) is installed here too, so
# the kernel, its /lib/modules/<ver>, and the boot initramfs all come from ONE apt
# transaction — they can never drift. linux-image-generic-hwe-22.04 mirrors
# Cowork's 6.8.0-*-generic and pulls linux-modules{,-extra} (hv_*, virtio, 9p,
# ext4/overlay/fuse). initramfs-tools is explicit (the kernel meta only recommends
# it, and --no-install-recommends would drop it) so the kernel postinst generates
# /boot/initrd.img-<ver>. image/build.sh extracts vmlinuz + initrd from /boot.
RUN apt-get update && apt-get install -y --no-install-recommends \
      busybox-static \
      python3 \
      python3-pip \
      nodejs \
      git \
      ripgrep \
      ca-certificates \
      openssh-server \
      initramfs-tools \
      linux-image-generic-hwe-22.04 \
    && rm -rf /var/lib/apt/lists/*

# /workspace is the only persistent mount (9p/virtiofs share from the host).
RUN mkdir -p /workspace

# init + guest config are installed by image/build.sh from image/guest/.
CMD ["/bin/bash"]
