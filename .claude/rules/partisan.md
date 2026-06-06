---
paths:
  - "packages/partisan/**"
---

# Agent loop — `packages/partisan` (Python/OpenHands)

`packages/partisan/cli_guest.py` is the sole in-guest agent, built on the **OpenHands SDK** (Python
≥3.12, `openhands-sdk`/`openhands-tools` 1.26.*, LiteLLM under it). The loop runs inside the cage, so
its hands are OpenHands' built-in coding tools (Bash/Read/Write/Edit/Glob/Grep) acting directly on the
guest fs — no broker round-trip for tools; only the model call escapes via the egress jail. The SDK is
embedded in-process (`Conversation` + `callbacks=[fn]`, no agent-server).

Flags: one-shot `--task` (drives `atelierctl agent`), persistent `--serve` (NDJSON over stdin/stdout,
driven by the Session Manager), `--resume <id>` for hibernate→resume; token streaming + mid-LLM-call
interrupt via async `arun()`. **stdout is NDJSON only** — the banner is suppressed and stray library
prints go to stderr. Model/key/`base_url` resolve `LLM_*` → `ATELIER_MODEL`/`ANTHROPIC_*`; the
`openhands/<model>` prefix is rejected (it routes to All-Hands' proxy). Uses `uv`.

```sh
cd packages/partisan
uv run cli_guest.py --task "create hello.txt" --workspace /tmp/ws   # one-shot
uv run ruff check . && uv run ruff format .   # strict lint + format (config in pyproject.toml)
npm run test:partisan        # from repo root: pytest + cross-language wire (scripts/test-partisan.mjs)
                             # --live adds streaming/interrupt/kill-and-resume against a real model
```

partisan ships on the runner volume for the target arch (`linux/amd64` on Windows, `linux/arm64` on
macOS): `image/build.sh runner` builds it via `image/agent/Dockerfile` and packs it at `/opt/atelier`,
mounted at `/opt`, not in the rootfs — so the desktop app does not ship it separately and it iterates
without a rootfs rebuild. partisan carries a `uv`-built venv at `packages/partisan/.venv` pinned to
the rootfs's system Python 3.12. The rootfs provides Python 3.12 + `tmux` (OpenHands' TerminalTool
needs tmux) plus a Node 22 runtime the agent can drive.
