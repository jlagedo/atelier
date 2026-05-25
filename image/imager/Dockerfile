# Imager image (CRIT-05 fix): a minimal Linux box with mke2fs so image/build.sh can
# populate the rootfs ext4 INSIDE Linux. The exported rootfs tar is extracted to a
# container-internal path (perms preserved) and mke2fs -d bakes correct ownership/modes
# into the image; only the opaque rootfs.ext4 blob is copied back to the (Windows) host,
# so no permission round-trip through a non-Linux filesystem ever happens.
#
# build.sh feeds the inputs in via `docker cp` (rootfs.tar, init.sh, runner, gvforwarder)
# and overrides the command, so this image only needs the tooling.
FROM ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends e2fsprogs \
    && rm -rf /var/lib/apt/lists/*
