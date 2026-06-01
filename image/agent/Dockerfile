# Build stage for the in-guest agent payload (Topology B, S5b.1, design.md §8). Produces
# /opt/atelier/packages/{protocol,partisan} with partisan's runtime venv for the target platform.
# The agent is NOT baked into the rootfs — image/build.sh exports /opt/atelier from this image and
# packs it into the runner volume (LABEL=runner, mounted read-only at /opt at runtime), so the
# agent iterates in seconds on the fast volume loop instead of forcing a full rootfs rebuild.
#
# Base is ubuntu:24.04 — the same system Python 3.12 the rootfs ships — so partisan's venv resolves
# its interpreter at runtime under /opt (read-only). --platform (passed by image/build.sh) pins the
# arch so native wheels can never drift from the guest.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# curl fetches the uv installer (and TLS via ca-certificates). python3 + python3-seccomp compile the
# sandbox seccomp filter below (F-13) for this image's native arch. python3-venv lets uv build
# partisan's venv against the system interpreter (the one that ships in the rootfs).
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      python3 python3-venv python3-seccomp \
    && rm -rf /var/lib/apt/lists/*

# Source only (no .venv/dist — image/build.sh stages it that way). partisan imports protocol by
# relative path, so the packages/ layout must be preserved.
COPY packages /opt/atelier/packages

# partisan (Topology B agent, docs/plans/openhands-adoption.md): build its Python venv here so the
# native wheels (pillow, tree-sitter, fakeredis) are baked for the target arch — the same
# guarantee --platform gives artisan's node_modules. uv installs to /usr/local/bin;
# UV_PYTHON_DOWNLOADS=never + --python /usr/bin/python3 force the venv onto the SYSTEM interpreter
# that also ships in the rootfs (same ubuntu:24.04 3.12), so the venv's python symlink resolves at
# runtime under /opt (read-only). --no-dev drops pytest; --frozen honors the committed uv.lock.
ENV UV_INSTALL_DIR=/usr/local/bin
# Drop the repo's dev .python-version (pins 3.14, the contributor's machine): the guest
# interpreter is whatever the rootfs ships (3.12), and the uv.lock is universal (requires-python
# >=3.12), so 3.12 is a supported resolution. --python pins both venv + sync to the system 3.12
# so uv never tries (and fails, downloads-off) to fetch the dev's pinned version.
RUN curl -LsSf https://astral.sh/uv/install.sh | sh \
    && cd /opt/atelier/packages/partisan \
    && rm -f .python-version \
    && UV_PYTHON_DOWNLOADS=never uv venv .venv --python /usr/bin/python3 \
    && UV_PYTHON_DOWNLOADS=never uv sync --frozen --no-dev --python /usr/bin/python3 \
    && uv cache clean

# Compile the agent sandbox's seccomp filter (docs/security/vm-sandbox.md F-13, closing F-01) from Docker's
# vendored default profile, evaluated as a no-capability process. Runs in this --platform
# container so the cBPF blob carries the target arch's syscall numbers by construction.
# image/build.sh exports /opt/seccomp/seccomp.bpf onto the runner volume next to the binary.
COPY seccomp /opt/seccomp-src
RUN mkdir -p /opt/seccomp \
    && python3 /opt/seccomp-src/compile-seccomp.py /opt/seccomp-src/default.json /opt/seccomp/seccomp.bpf

# Never run as a container — image/build.sh `docker create` + `docker cp /opt/atelier` only.
CMD ["/bin/true"]
