# Build stage for the in-guest agent payload (Topology B, S5b.1, design.md §8). Produces
# /opt/atelier/packages/{agent,provider,protocol} with the agent's runtime node_modules for
# the target platform. The agent is NOT baked into the rootfs — image/build.sh exports
# /opt/atelier from this image and packs it into the guestd volume (LABEL=guestd, mounted
# read-only at /opt at runtime), so the agent iterates in seconds on the fast volume loop
# instead of forcing a full rootfs rebuild.
#
# Base is ubuntu:24.04 + NodeSource 22.x — identical to image/rootfs/Dockerfile — so the
# baked node_modules are ABI/glibc-matched to the Node 22 runtime that actually runs the
# agent (via tsx) in the rootfs. --platform (passed by image/build.sh) pins the arch so
# native deps can never drift from the guest. The agent imports provider/protocol by
# relative path, so the packages/ layout must be preserved.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# gnupg + curl are what the NodeSource setup script needs to add its apt repo; ca-certificates
# for TLS to deb.nodesource.com and (later) npm registry. python3 + python3-seccomp compile the
# sandbox seccomp filter below (F-13) for this image's native arch.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      gnupg \
      curl \
    && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs python3 python3-seccomp \
    && rm -rf /var/lib/apt/lists/*

# Source only (no node_modules/dist — image/build.sh stages it that way); install the agent's
# runtime deps here so they're baked with the right platform binaries.
COPY packages /opt/atelier/packages
RUN cd /opt/atelier/packages/agent \
    && npm ci --omit=dev \
    && npm cache clean --force

# Compile the agent sandbox's seccomp filter (docs/security.md F-13, closing F-01) from Docker's
# vendored default profile, evaluated as a no-capability process. Runs in this --platform
# container so the cBPF blob carries the target arch's syscall numbers by construction.
# image/build.sh exports /opt/seccomp/seccomp.bpf onto the guestd volume next to the binary.
COPY seccomp /opt/seccomp-src
RUN mkdir -p /opt/seccomp \
    && python3 /opt/seccomp-src/compile-seccomp.py /opt/seccomp-src/default.json /opt/seccomp/seccomp.bpf

# Never run as a container — image/build.sh `docker create` + `docker cp /opt/atelier` only.
CMD ["/bin/true"]
