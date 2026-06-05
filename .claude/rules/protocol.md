---
paths:
  - "tools/protogen/**"
  - "packages/protocol/**"
---

# Protocol codegen — `tools/protogen`

`packages/protocol/schema/protocol.json` is the canonical Hop-2 protocol. `tools/protogen` (zero-dep
Node) generates TS + Go from it; outputs are gitignored — regenerate, don't hand-edit. Generated
source is imported by module path, so it stays in-tree.

```sh
npm run protogen        # from repo root
# writes packages/protocol/src/index.ts  and  services/pkg/protocol/protocol.go
```

TODO: emit Zod schemas alongside the TS interfaces.
