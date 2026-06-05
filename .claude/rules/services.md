---
paths:
  - "services/**"
---

# Host services — `services` (Go)

Module: `github.com/jlagedo/atelier/services`. Protocol: JSON-RPC 2.0 with Content-Length framing,
over a named pipe on Windows / a unix socket for dev. Three binaries under `cmd/`: **`atelierd`** (the
privileged broker), **`runner`** (the in-VM daemon, shipped on the ro runner volume — `image/build.sh
runner` — mounted as a second disk, not in the rootfs, so it iterates without an image rebuild),
**`atelierctl`** (dev CLI).

```sh
cd services
go build ./... && go test ./... && golangci-lint run    # strict config: services/.golangci.yml (govet+gofmt folded in)
GOOS=windows go build ./...     # verify the Windows named-pipe / HCS paths compile

# dev end-to-end (unix socket, no VM):
go run ./cmd/atelierd  -addr /tmp/atelierd.sock &
go run ./cmd/atelierctl -addr /tmp/atelierd.sock getStatus
```

## macOS (Apple Silicon): CGO + codesign

The broker drives Apple's Virtualization.framework via the `Code-Hex/vz` cgo binding
(`internal/vmm/driver_darwin.go`). Darwin builds need `CGO_ENABLED=1` + Xcode Command Line Tools, and
the broker must be codesigned with `com.apple.security.virtualization`
(`services/packaging/darwin/atelier-vm.entitlements`) under the hardened runtime — VZ refuses an
unsigned broker, and cgo invalidates the signature on every rebuild. Build + sign via the
orchestrator, not a bare `go build`:

```sh
npm run build:all -- --only=host      # protogen -> cgo build host+atelierctl -> codesign -> build/debug/
build/debug/atelierd  -addr /tmp/atelierd.sock &
B=build/debug/image/darwin-arm64-vz
build/debug/atelierctl -addr /tmp/atelierd.sock createVM -id vm0 \
  -kernel $B/vmlinuz -initrd $B/initrd -rootfs $B/rootfs.raw
build/debug/atelierctl -addr /tmp/atelierd.sock startVM -id vm0   # serial boot log -> broker stderr
build/debug/atelierctl -addr /tmp/atelierd.sock stopVM  -id vm0
```

## Debug console (debug builds only, macOS/VZ)

The guest has no inbound path (vsock-only, egress default-deny, no sshd). Debug builds wire a second
virtio console (`/dev/hvc1`) bridged to a unix socket for an interactive root shell into the VM: the
broker adds `atelier.debug=1` to the kernel cmdline and `init.sh` spawns a root shell on hvc1. Attach
with `atelierctl console -id vm0` (raw-mode TTY; Ctrl-] detaches without ending the shell). The socket
appears automatically at boot — no env var.

```sh
build/debug/atelierd -addr /tmp/atelierd.sock &   # then createVM/startVM as above
build/debug/atelierctl console -id vm0            # interactive root shell on hvc1
```

This shell is root, outside the bwrap/Landlock/seccomp cage, so it is gated at build time: the host
code is behind the `debugconsole` Go build tag (`build:all` sets it only for `--config=debug`) and the
`init.sh` block is stripped from the release rootfs (`image/build.sh` keyed on `ATELIER_CONFIG`). A
release build contains neither — there is no runtime flag to flip.

## End-to-end battery — `scripts/e2e-host.mjs`

```sh
npm run e2e:host                      # build debug if missing, boot vm0, drive all 12 doors + agent
npm run e2e:host -- --config=release  # against build/release/
npm run e2e:host -- --skip-build      # reuse build/<config>/ as-is (fast-fail if incomplete)
```

Spawns the shipped broker over a unix socket and exercises every door + the in-guest agent loop
through `atelierctl` — the real Hop-2 wire, which the Go unit tests (fake drivers) and the S7 probe
(`internal/vmm/s7_probe_darwin_test.go`) don't cover. It covers both share models (legacy `/workspace`
via the Files door; concurrent `/sessions/<tag>` with isolation, arbitrary targets, sibling-safe
detach), the egress jail (default-deny blocks, allow reaches the model), and host↔guest bridging both
ways. A real
boot, so VZ + a codesigned broker + the image bundle are required; the agent check needs
`ANTHROPIC_API_KEY` (it fails the suite if absent).

## `internal/` packages + the 12 doors

`broker` (policy gate + audit + Files/Network doors), `hcs` (`computecore.dll` bindings), `vmm`
(lifecycle + guest/console wiring), `rpc` (JSON-RPC codec/transport/notifications), `vsock` (hvsocket
dialing), `netjail` (default-deny egress via gvisor-tap-vsock). The 12 doors live in `pkg/protocol`
(generated): `getStatus`, `createVM`, `startVM`, `stopVM`, `exec`, `execInput`, `attachWorkspace`,
`detachWorkspace`, `readFile`, `writeFile`, `setEgressPolicy`, `setTime`.

## Conventions

- Windows/Linux-only code lives behind `//go:build` tags with a sibling stub
  (e.g. `internal/rpc/transport_*.go`, `internal/hcs/hcs_*.go`, `cmd/runner/*_linux.go` +
  `*_other.go`) so `go build ./...` works on either host.
- `internal/broker` is the containment chokepoint: every capability use passes the policy gate
  (allow/ask/deny) + audit log before acting. The Files door is workspace-relative and jails paths
  (rejects `..` and escaping symlinks).
- `go.mod` `go` directive is pinned to the installed toolchain (1.25).
