# services

One Go module: the privileged host service + in-VM daemon + dev CLI.

**Scaffold status:** the Hop-2 broker seam is runnable. `host` serves JSON-RPC 2.0
(Content-Length framed) over a named pipe (Windows) or a unix socket (dev); `vmctl`
drives it from a terminal; `getStatus` works end-to-end; VM lifecycle / file methods
are gated, audited stubs. HCS (hcsshim) and the guest transport land in later milestones.

> **Go version:** module targets the locally installed toolchain (`go 1.24.7`). Latest
> stable is Go 1.26 — bump the `go` directive when the build host upgrades. Deps use
> latest (`go-winio v0.6.2`).

## Layout

| Path | Role |
|---|---|
| `cmd/host` | broker service — JSON-RPC server (LocalSystem on Windows) |
| `cmd/vmctl` | dev CLI client — one RPC call, prints the result [M0-M2] |
| `cmd/guestd` | in-VM daemon stub (hvsocket server) [M5b] |
| `internal/rpc` | JSON-RPC 2.0 + Content-Length framing + transport (pipe/socket) |
| `internal/broker` | policy gate (allow/ask/deny) + audit log — the chokepoint |
| `internal/hcs` | HCS driver: Windows impl (hcsshim, TODO M1) + dev stub |
| `internal/netjail` | egress control [M4] |
| `internal/vm` | VM manager: hvsocket RPC, serial console, 9p [M2-M3] |
| `pkg/protocol` | generated Go structs for the IPC schema (gitignored) |

## Build / run / test

```sh
cd services
go build ./...                       # build everything (this host)
go test ./...                        # unit tests (rpc framing + dispatch)
go vet ./... && gofmt -l .           # vet + format check
GOOS=windows go build ./...          # cross-compile the Windows (named-pipe/HCS) paths

# end-to-end demo (dev, unix socket):
go run ./cmd/host -addr /tmp/atelier-host.sock &   # start the broker
go run ./cmd/vmctl -addr /tmp/atelier-host.sock getStatus
```
