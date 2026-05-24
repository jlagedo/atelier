#!/usr/bin/env bash
#
# Build the utility-VM image bundle (design.md §7): a pinned set of
# kernel (vmlinuz) + boot initramfs (initrd) + Ubuntu rootfs (ext4 in a VHD),
# mirroring Cowork's claudevm.bundle. Sources live in image/{rootfs,initrd,kernel,guest};
# outputs go to image/bundle/ (gitignored). The output BASE is overridable via ATELIER_OUT_BASE
# (the build-all.mjs orchestrator points it at ../build/<config>/image so all artifacts land in
# one tree); the per-target subdir ($TARGET) is always appended.
#
# The matched kernel (linux-image-virtual-hwe-24.04) + its /lib/modules + the boot
# initramfs are produced by the rootfs Docker build (one apt transaction, so the
# coupling rule of design.md §7 holds by construction). `rootfs` builds the ext4;
# `kernel`/`initrd` extract + pin vmlinuz/initrd from /boot of that same tree.
#
# Usage: image/build.sh {check|rootfs|initrd|kernel|guestd|bundle|all|clean}
set -euo pipefail

cd "$(dirname "$0")"

UBUNTU_VERSION="24.04"

# A build TARGET selects everything platform-specific in one place: the guest ARCH, the
# Docker build platform, the Go cross-compile GOARCH, the disk format, and the per-target
# output dir. Default is the Windows/Hyper-V bundle so existing invocations are unchanged.
# --platform pins the rootfs arch to the TARGET (not the Docker host), so the apt kernel +
# baked node_modules can never drift from the GOARCH-built guestd/gvforwarder.
TARGET="${TARGET:-windows-amd64-hyperv}"
case "$TARGET" in
  windows-amd64-hyperv) ARCH="x86_64";  DOCKER_PLATFORM="linux/amd64"; GOARCH="amd64"; DISK="vhd" ;;
  darwin-arm64-vz)      ARCH="aarch64"; DOCKER_PLATFORM="linux/arm64"; GOARCH="arm64"; DISK="raw" ;;
  *) echo "image: unknown TARGET '$TARGET' (want: windows-amd64-hyperv | darwin-arm64-vz)" >&2; exit 2 ;;
esac
OUT="${ATELIER_OUT_BASE:-bundle}/$TARGET"   # base overridable (orchestrator -> ../build/<config>/image)
WORK=".work/$TARGET"      # per-target: never mix arch trees/tars/binaries across targets
ROOTFS_TAG="atelier-rootfs:${UBUNTU_VERSION}-${ARCH}"

log() { printf '\033[1;34m[image]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[image] error:\033[0m %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# stage_pkg DEST PKG — copy packages/PKG SOURCE (no node_modules/dist/.git) into
# DEST/packages/PKG. Used to assemble a small, controlled Docker build context (the repo
# root is too big to send — rootfs.vhd, .git, node_modules).
stage_pkg() {
  local dest="$1" p="$2"
  mkdir -p "$dest/packages/$p"
  ( cd "../packages/$p" && tar --exclude=node_modules --exclude=dist --exclude=.git -cf - . ) \
    | ( cd "$dest/packages/$p" && tar -xf - )
}

# stage_agent_ctx assembles the in-guest agent's Docker build context in $WORK/agentctx so
# image/agent/Dockerfile can COPY + npm-install the agent (Topology B, S5b.1). The agent is
# packed into the guestd volume (cmd_guestd), NOT baked into the rootfs. npm install runs INSIDE
# that build (linux/$GOARCH via --platform) so the node_modules has the right platform binaries.
stage_agent_ctx() {
  # protocol/src is generated (gitignored). It must exist before we stage it.
  [ -f "../packages/protocol/src/index.ts" ] \
    || die "packages/protocol/src is missing — run 'npm run protogen' at the repo root first"
  rm -rf "$WORK/agentctx"
  mkdir -p "$WORK/agentctx/packages"
  cp agent/Dockerfile "$WORK/agentctx/Dockerfile"
  stage_pkg "$WORK/agentctx" agent
  stage_pkg "$WORK/agentctx" provider
  stage_pkg "$WORK/agentctx" protocol
}

cmd_check() {
  log "target:  $TARGET (arch=$ARCH platform=$DOCKER_PLATFORM goarch=$GOARCH disk=$DISK out=$OUT)"
  log "volume:  guestd + in-guest agent shipped together on one volume (guestd.$([ "$DISK" = raw ] && echo raw || echo vhd)), not baked into the rootfs"
  log "tool readiness:"
  for t in docker go qemu-img sha256sum; do
    if have "$t"; then printf '  ok    %s\n' "$t"; else printf '  MISSING %s\n' "$t"; fi
  done
  log "all stages need docker + go; the ext4 is populated inside a Linux 'imager'"
  log "container (mke2fs runs there, not on the host) so file perms survive. qemu-img"
  log "is needed only for the VHD disk format (windows-* targets), not raw (darwin-* / VZ)."
}

# ensure_tree builds the rootfs container and exports its filesystem into
# $WORK/rootfs (with guest init installed), once per invocation. The exported tree
# carries the matched kernel: /boot/vmlinuz-<ver>, /boot/initrd.img-<ver>, and
# /lib/modules/<ver>. cmd_rootfs/cmd_kernel/cmd_initrd all build on it.
TREE_READY=0
ensure_tree() {
  [ "$TREE_READY" = 1 ] && return 0
  have docker || die "docker not found (needed to build/export the Ubuntu rootfs + kernel)"
  mkdir -p "$WORK/rootfs" "$OUT"

  # The rootfs no longer COPYs the agent (it ships on the guestd volume — cmd_guestd), so its
  # build context is just image/rootfs/ (the Dockerfile). No package staging needed here.
  log "building rootfs container image ($ROOTFS_TAG, $DOCKER_PLATFORM)"
  docker build --platform "$DOCKER_PLATFORM" -t "$ROOTFS_TAG" rootfs

  log "exporting container filesystem (tar preserves perms; the ext4 is built from it in Linux)"
  cid="$(docker create "$ROOTFS_TAG")"
  trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' RETURN
  docker export -o "$WORK/rootfs.tar" "$cid"

  # Host-side extraction is ONLY to pull /boot (vmlinuz+initrd) and /lib/modules in
  # cmd_kernel/cmd_initrd — opaque blobs whose perms don't matter, so the loss on a
  # Windows fs is fine. The BOOTABLE rootfs ext4 is populated from rootfs.tar INSIDE
  # Linux (cmd_rootfs) so its ownership/modes are correct — the CRIT-05 fix.
  rm -rf "$WORK/rootfs"; mkdir -p "$WORK/rootfs"
  tar -x -C "$WORK/rootfs" -f "$WORK/rootfs.tar"

  # gvforwarder is baked into the rootfs (installed by the imager step below); guestd + the
  # in-guest agent are NOT — they ship on the guestd volume built separately by cmd_guestd, so
  # they iterate without an image rebuild. gvforwarder needs the Go toolchain + $WORK/bin.
  have go || die "go not found (needed to cross-compile the guest network forwarder, gvforwarder)"
  mkdir -p "$WORK/bin"

  # Guest network forwarder (gvforwarder, design.md §8 Hop 3 channel 2 — S4.1):
  # gvisor-tap-vsock's cmd/vm at the exact version services/go.mod pins (go install
  # pkg@version resolves against the library's own module, independent of our go.mod).
  # guestd supervises it at boot, now with -preexisting since networking is static (S4.1).
  local gvbin gvver gvpath gvsrc
  gvver="$(cd ../services && go list -m -f '{{.Version}}' github.com/containers/gvisor-tap-vsock)"
  log "building guest network forwarder (gvforwarder ${gvver}, linux/$GOARCH static)"
  gvbin="$(pwd)/$WORK/bin"
  # `go install pkg@version` resolves against the library's own module (independent of our
  # go.mod/go.sum), but it REFUSES a set GOBIN when cross-compiling — and building linux on a
  # macOS/Windows host IS cross (host GOOS != linux). So install into a build-local GOPATH
  # (cross binaries land in bin/<goos>_<goarch>/, native in bin/) while reusing the shared
  # module cache, then move the located binary out. (The Windows bundle builds in WSL2, a
  # linux host, where this was non-cross — which is why a set GOBIN worked there.)
  gvpath="$(pwd)/$WORK/gopath"
  ( env -u GOBIN GOPATH="$gvpath" GOMODCACHE="$(go env GOMODCACHE)" \
        GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 \
      go install -trimpath "github.com/containers/gvisor-tap-vsock/cmd/vm@${gvver}" )
  gvsrc="$(find "$gvpath/bin" -name vm -type f 2>/dev/null | head -n1)"
  [ -n "$gvsrc" ] || die "gvforwarder: built 'vm' binary not found under $gvpath/bin"
  mv -f "$gvsrc" "$gvbin/gvforwarder"

  log "building imager image (e2fsprogs) for in-Linux ext4 population"
  docker build --platform "$DOCKER_PLATFORM" -t atelier-imager imager

  TREE_READY=1
}

# kver prints the matched kernel version (from /boot/vmlinuz-<ver>), or nothing.
# Glob (not ls|head) so an absent tree doesn't trip pipefail/set -e.
kver() {
  local g
  for g in "$WORK"/rootfs/boot/vmlinuz-*; do
    [ -e "$g" ] || continue
    echo "${g##*/vmlinuz-}"
    return 0
  done
}

cmd_rootfs() {
  ensure_tree

  # Populate the ext4 INSIDE Linux (CRIT-05 fix): extract the exported rootfs tar to a
  # container-internal path (perms preserved), install the guest init + daemons, normalize
  # sensitive perms + bake the static resolv.conf, then mke2fs -d. Inputs go in via
  # `docker cp`; only the opaque rootfs.ext4 blob is copied back out — no perm round-trip
  # through the Windows fs. mke2fs -d needs no mount/loop/privilege. 4G leaves headroom for
  # the matched /lib/modules(+extra) the kernel installs.
  log "building ext4 image inside Linux (perms preserved; root mounted read-only at runtime)"
  local build='set -eu
mkdir -p /rootfs
tar -x -C /rootfs -f /rootfs.tar
install -D -m 0755 /init.sh     /rootfs/sbin/init
install -D -m 0755 /gvforwarder /rootfs/usr/sbin/gvforwarder
mkdir -p /rootfs/opt
printf "nameserver 192.168.127.1\n" > /rootfs/etc/resolv.conf
chmod 0755 /rootfs/usr /rootfs/usr/bin /rootfs/usr/sbin /rootfs/bin /rootfs/sbin /rootfs/etc
chmod 0644 /rootfs/etc/passwd
chmod 0640 /rootfs/etc/shadow
chown -R 0:0 /rootfs/usr /rootfs/etc
mke2fs -q -t ext4 -L atelier-root -d /rootfs -r 1 -N 0 -m 1 /rootfs.ext4 4G'
  local icid; icid="$(docker create atelier-imager bash -c "$build")"
  docker cp "$WORK/rootfs.tar"      "$icid:/rootfs.tar"
  docker cp "$WORK/bin/gvforwarder" "$icid:/gvforwarder"
  docker cp guest/init.sh           "$icid:/init.sh"
  if ! docker start -a "$icid"; then
    docker rm -f "$icid" >/dev/null 2>&1 || true
    die "imager failed to build the ext4"
  fi
  docker cp "$icid:/rootfs.ext4" "$WORK/rootfs.ext4"
  docker rm -f "$icid" >/dev/null 2>&1 || true

  if [ "$DISK" = raw ]; then
    # VZDiskImageStorageDeviceAttachment takes the raw ext4 as-is (validation #6) — the
    # mke2fs blob IS a raw disk image, so there's nothing to convert.
    log "emitting raw ext4 disk for $TARGET (VZ attaches it as-is; no qemu-img needed)"
    cp "$WORK/rootfs.ext4" "$OUT/rootfs.raw"
  elif have qemu-img; then
    log "converting ext4 -> VHD (hcsshim PreferredRootFSType=vhd, design.md §7)"
    qemu-img convert -f raw -O vpc "$WORK/rootfs.ext4" "$OUT/rootfs.vhd"
  else
    log "qemu-img missing — leaving raw ext4 at $WORK/rootfs.ext4 (convert to VHD later)"
  fi
  log "rootfs done"
}

cmd_kernel() {
  ensure_tree
  log "extracting + pinning kernel"
  # Pass DISK so raw (VZ) targets get a decompressed arm64 Image; vhd keeps the
  # compressed vmlinuz HCS boots directly.
  ./kernel/fetch-kernel.sh "$OUT" "$WORK/rootfs" "$DISK"
}

cmd_initrd() {
  ensure_tree
  # Full boot initramfs: initramfs-tools' default MODULES=most, generated by the
  # kernel postinst inside the Docker build (image/initrd/modules.conf is reference
  # only under this policy). We just extract + pin it.
  log "extracting + pinning boot initramfs"
  src="$(ls -1 "$WORK/rootfs"/boot/initrd.img-* 2>/dev/null | head -n1 || true)"
  [ -n "$src" ] || die "no /boot/initrd.img-* in the rootfs tree (initramfs-tools/kernel not installed?)"
  cp "$src" "$OUT/initrd"
  sha256sum "$OUT/initrd" | awk '{print $1}' > "$OUT/initrd.origin"
  log "initrd: $(basename "$src") -> $OUT/initrd"
}

cmd_bundle() {
  mkdir -p "$OUT"
  log "assembling bundle in $OUT/ (pin kernel+initrd+rootfs with sha256 .origin markers)"
  written=0
  for f in vmlinuz initrd rootfs.vhd rootfs.raw rootfs.ext4 guestd.vhd guestd.raw; do
    if [ -f "$OUT/$f" ]; then
      sha256sum "$OUT/$f" | awk '{print $1}' > "$OUT/$f.origin"
      printf '  pinned %s -> %s.origin\n' "$f" "$f"
      written=1
    fi
  done
  [ "$written" = 1 ] || log "nothing to bundle yet (run rootfs/kernel/initrd first)"
  k="$(kver)"
  cat > "$OUT/manifest.txt" <<EOF
atelier vm bundle
target: ${TARGET}
ubuntu: ${UBUNTU_VERSION}
arch:   ${ARCH}
disk:   ${DISK}
kernel: ${k:-unknown}
built:  $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
}

# cmd_guestd builds the guestd volume — one ro ext4 shipped as a separate disk so guestd AND the
# in-guest agent iterate in seconds without rebuilding the rootfs (design.md §7/§8). It carries
# both: guestd (/opt/guestd/guestd, the vsock RPC daemon) and the agent (/opt/atelier, Topology B
# — code + node_modules). init.sh mounts the volume ro at /opt. Self-contained vs the rootfs (no
# ensure_tree, no apt/kernel), but the agent's node_modules DO need a target-arch npm install, so
# this builds image/agent/Dockerfile (npm ci) and exports /opt/atelier from it. Cross-compile
# guestd, pack a labeled ext4 INSIDE the imager (perms preserved, sized from the staged tree),
# then emit raw (VZ attaches as-is) or convert to VHD (HCS SCSI disk), mirroring cmd_rootfs.
cmd_guestd() {
  have go     || die "go not found (needed to cross-compile guestd, services/cmd/guestd)"
  have docker || die "docker not found (needed to build the agent payload + mke2fs the volume)"
  mkdir -p "$WORK/bin" "$OUT"
  log "building guest daemon (guestd, linux/$GOARCH static) for the guestd volume"
  local gout; gout="$(pwd)/$WORK/bin/guestd"
  ( cd ../services && env GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath -o "$gout" ./cmd/guestd )

  log "staging agent build context (agent/Dockerfile + packages/{agent,provider,protocol})"
  stage_agent_ctx
  local agent_tag="atelier-agent:${UBUNTU_VERSION}-${ARCH}"
  log "building agent payload image ($agent_tag, $DOCKER_PLATFORM) — npm ci for linux/$GOARCH"
  docker build --platform "$DOCKER_PLATFORM" -t "$agent_tag" "$WORK/agentctx"
  log "exporting agent tree (/opt/atelier) from the payload image"
  local acid; acid="$(docker create "$agent_tag")"
  rm -rf "$WORK/agent"; mkdir -p "$WORK/agent"
  docker cp "$acid:/opt/atelier" "$WORK/agent/atelier"   # -> $WORK/agent/atelier/packages/...
  docker rm -f "$acid" >/dev/null 2>&1 || true

  log "building imager image (e2fsprogs) for in-Linux ext4 population"
  docker build --platform "$DOCKER_PLATFORM" -t atelier-imager imager

  log "packing guestd volume (LABEL=guestd; /guestd + /atelier) — ext4 via mke2fs -d"
  # Combined payload: guestd binary + the agent tree, both root-owned (read-only at runtime; the
  # agent drops to uid 1001 via bwrap). Size from the staged tree (node_modules dominates) + 128M
  # headroom so mke2fs -d always fits. node_modules has thousands of tiny files, so the default
  # inode budget (~1/16KB) under-provisions — set -N from the actual file count + margin or the
  # populate fails with "out of inodes". mke2fs -d needs no mount/loop/privilege.
  local build='set -eu
mkdir -p /stage/guestd
install -D -m 0755 /guestd /stage/guestd/guestd
cp -a /atelier /stage/atelier
chown -R 0:0 /stage
sz=$(($(du -sm /stage | cut -f1) + 128))
ninodes=$(($(find /stage | wc -l) + 4096))
mke2fs -q -t ext4 -L guestd -d /stage -r 1 -N "$ninodes" -m 0 /guestd.ext4 "${sz}M"'
  local icid; icid="$(docker create atelier-imager bash -c "$build")"
  docker cp "$WORK/bin/guestd"    "$icid:/guestd"
  docker cp "$WORK/agent/atelier" "$icid:/atelier"
  if ! docker start -a "$icid"; then
    docker rm -f "$icid" >/dev/null 2>&1 || true
    die "imager failed to build the guestd volume"
  fi
  docker cp "$icid:/guestd.ext4" "$WORK/guestd.ext4"
  docker rm -f "$icid" >/dev/null 2>&1 || true

  if [ "$DISK" = raw ]; then
    log "emitting raw guestd volume for $TARGET (VZ attaches it as-is)"
    cp "$WORK/guestd.ext4" "$OUT/guestd.raw"
  elif have qemu-img; then
    log "converting guestd volume ext4 -> VHD (HCS SCSI disk)"
    qemu-img convert -f raw -O vpc "$WORK/guestd.ext4" "$OUT/guestd.vhd"
  else
    die "qemu-img missing — needed to convert the guestd volume to VHD for $TARGET"
  fi
  log "guestd volume done"
}

# rootfs first builds the tree once; kernel/initrd extract from the retained
# $WORK/rootfs (ensure_tree is memoized for the invocation). guestd ships as its own
# volume (cmd_guestd), built before the bundle pins everything.
cmd_all() { cmd_rootfs; cmd_kernel; cmd_initrd; cmd_guestd; cmd_bundle; }

# OUT/WORK are per-target (bundle/$TARGET, .work/$TARGET), so removing them wholesale never
# touches bundle/README.md or the other target's output.
cmd_clean() { rm -rf "$WORK" "$OUT"; log "cleaned $TARGET"; }

case "${1:-}" in
  check)  cmd_check ;;
  rootfs) cmd_rootfs ;;
  initrd) cmd_initrd ;;
  kernel) cmd_kernel ;;
  guestd) cmd_guestd ;;
  bundle) cmd_bundle ;;
  all)    cmd_all ;;
  clean)  cmd_clean ;;
  *) echo "usage: $0 {check|rootfs|initrd|kernel|guestd|bundle|all|clean}" >&2; exit 2 ;;
esac
