# Rootfs source for the utility VM (design.md §7). We don't run this as a
# container — `image/build.sh rootfs` does `docker export` of it and pours the
# result into an ext4 VHD. glibc (Ubuntu) is deliberate: Python wheels just work.
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# Manifest (design.md §7): init shell, python3+pip, node, git, ripgrep, CA certs.
# The guest bridge daemon + TS agent CLI are layered in later milestones.
RUN apt-get update && apt-get install -y --no-install-recommends \
      busybox-static \
      python3 \
      python3-pip \
      nodejs \
      git \
      ripgrep \
      ca-certificates \
      openssh-server \
    && rm -rf /var/lib/apt/lists/*

# /workspace is the only persistent mount (9p/virtiofs share from the host).
RUN mkdir -p /workspace

# init + guest config are installed by image/build.sh from image/guest/.
CMD ["/bin/bash"]
