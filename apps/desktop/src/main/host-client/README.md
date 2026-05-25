# apps/desktop/src/main/host-client

Hop 2: named-pipe JSON-RPC client → Go host service (`\\.\pipe\atelierd`).

- `client.ts` — low-level `PipeClient`: one connection, Content-Length framing,
  unary `call` + streaming `callStream`. Exports the pure codec (`encodeFrame`,
  `parseFrames`) for tests.
- `index.ts` — `HostClient` facade: typed methods (getStatus, createVM, start/stop,
  attach/detachWorkspace, setEgressPolicy, execInput) + `execStream` for a
  long-lived run. Opens a fresh connection **per call/run** so concurrent sessions'
  streamed `exec/output` notifications never mix.
- `types.ts` — protocol + agent-loop (`--serve` NDJSON) types, inlined to avoid a
  cross-package build dep on the generated `packages/protocol`. Keep in sync with
  `packages/protocol/schema/protocol.json` and `packages/artisan/src/cli-guest.ts`.
