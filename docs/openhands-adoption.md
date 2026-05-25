# OpenHands SDK adoption review

| Field | Detail |
|---|---|
| Status | Review and plan — not yet executed |
| Objective | Replace Atelier's Anthropic-locked in-guest agent with a provider-agnostic engine |
| Approach | Put the OpenHands Software Agent SDK behind Atelier's existing NDJSON serve seam |
| Validated | OpenHands SDK v1.23.0; Atelier integration anchors confirmed at `file:line` (2026-05) |

This document evaluates replacing Atelier's in-guest agent — today built on
`@anthropic-ai/claude-agent-sdk` (TypeScript) — with an engine built on the OpenHands Software
Agent SDK (Python). The motivation is model-provider freedom: the Claude Agent SDK targets
Anthropic models only, while Atelier must run against any provider, including company models hosted
internally (on-prem / in-tenant). It maps OpenHands onto Atelier's current integration seams (with
verified `file:line` anchors), classifies each OpenHands module as *use / adapt / skip*, and lays
out a phased adoption.

> **Sources** (verified against PyPI v1.23.0, current as of 2026-05): OpenHands Software Agent SDK
> paper (arXiv 2511.03690, MLSys 2026); `docs.openhands.dev/sdk`; repo `OpenHands/software-agent-sdk`;
> `pypi.org/project/openhands-sdk`. MIT license; Python ≥ 3.12. The core is Python application code,
> but its dependency tree is *not* strictly native-free — `pillow` and `fakeredis[lua]` carry compiled
> components (shipped as prebuilt wheels). This is the V1 modular rewrite, distinct from the legacy
> `openhands-ai` monolith.

---

## 1. Guiding principles

**The driver is model-provider freedom.** The Claude Agent SDK targets Anthropic models only;
Atelier needs every provider — OpenAI, Bedrock, Azure, and especially internally-hosted company
models (data residency, cost, control). LiteLLM, via OpenHands' `LLM`, provides this. The objection
is to *model* lock-in, not to using an SDK.

**Don't trade one lock-in for another.** The corollary guardrail: avoid swapping Anthropic model
lock-in for OpenHands *engine* lock-in. We keep an abstraction boundary we own and place OpenHands
behind it as a swappable implementation.

**That boundary already exists — the NDJSON serve protocol**
(`packages/artisan/src/cli-guest.ts:18-33`). The host Session Manager neither knows nor cares which
engine sits behind the wire. The plan is therefore to keep the NDJSON seam and put OpenHands behind
it.

**The two architectures are the same shape.** OpenHands' `agent-server` + `RemoteConversation` +
`RemoteWorkspace` + WebSocket event stream is structurally identical to Atelier's Session Manager ↔
in-guest agent over NDJSON, and its `Workspace` abstraction (agent loop in one place, execution
elsewhere) is Atelier's VM-as-cage (Topology B). This adopts a more capable version of patterns
already in place, rather than fighting the grain.

### Goals

1. **Provider-agnostic** — run against any model provider, including internally-hosted company
   models, not just Anthropic.
2. **Keep WORK working** — the current WORK feature (an agent driving the user's daily agentic
   needs) must keep working on the new engine.
3. **Expand later** — grow from the chat agentic runtime into tasks, process, and user-authored
   custom skills.

---

## 2. OpenHands SDK — module map

Four composable PyPI packages (`pip install openhands-sdk`, etc.; import root `openhands.*`):

| Package | Import | Contains | Notable deps |
|---|---|---|---|
| **openhands-sdk** (core) | `openhands.sdk` | `Agent`, `Conversation`, `LLM`, `Tool` / `ToolDefinition`, `Action` / `Observation`, event system, MCP, security, condensers | `litellm`, `pydantic>=2`, `fastmcp`, `httpx[socks]`, `tenacity`, plus `websockets`, `pillow`, `fakeredis[lua]`, `joserfc`, `python-frontmatter`, `agent-client-protocol`, … — installable via wheels, but not dependency-free (`pillow` / `fakeredis[lua]` carry native components) |
| **openhands-tools** | `openhands.tools` | Concrete tools: `TerminalTool`, `FileEditorTool`, `TaskTrackerTool`, `GrepTool`, browser | `browser-use` (heavy — pulls Playwright/Chromium; needed *only* for the browser tool) |
| **openhands-workspace** | `openhands.workspace` | `LocalWorkspace`, `DockerWorkspace`, `RemoteWorkspace` / `RemoteAPIWorkspace` | docker tooling — depends on `openhands-agent-server` (pulls FastAPI/uvicorn), so it is *not* installable independently of the server |
| **openhands-agent-server** | `openhands.agent_server` | FastAPI REST + WebSocket server, remote conversation lifecycle | `fastapi`, `uvicorn`, `websockets`, `docker` |

(`openhands-cli` is a separate TUI repo, not one of the four.)

### Capability summary (by subsystem)

- **LLM layer — provider-agnostic via LiteLLM (100+ providers).** `LLM(model="anthropic/claude-...",
  api_key=..., base_url=...)`; provider chosen by LiteLLM model prefix → Anthropic, OpenAI, Bedrock,
  Azure, Ollama/vLLM (local), any OpenAI-compatible endpoint. Env knobs `LLM_API_KEY`, `LLM_MODEL`,
  `LLM_BASE_URL`. Supports Chat Completions and the Responses API; reasoning blocks captured.
  Multi-LLM routing via `RouterLLM` (subclass, implement `select_llm(messages)`). Non-function-calling
  models handled by a prompt+regex mixin.
- **Conversation & events — event-sourced, immutable, replayable.** `Conversation(agent=...,
  workspace=..., persistence_dir=..., conversation_id=...)`; methods `send_message()`, `run()`.
  `ConversationState` is the only mutable object: metadata (`agent_status`, `stats`,
  `confirmation_policy`) plus an append-only `EventLog` (single source of truth). Typed events:
  `MessageEvent`, `ActionEvent`, `ObservationEvent`, `AgentErrorEvent`, `CondensationEvent`.
  Persistence: metadata → `base_state.json`, events → individual JSON files; resume = load base_state
  and replay events (agents auto-detect and continue incomplete conversations). The immutable log is
  exactly the audit + replay property we want.
- **Agent — immutable, serializable spec.** `Agent(llm=..., tools=[...], ...)`: stateless immutable
  spec (LLM settings + tool specs + security policy + content), serializable across process
  boundaries. `AgentContext` (system/user prefixes & suffixes + `Skill`s) is the system-prompt
  customization seam. Presets: `openhands.tools.preset.default.get_default_agent`.
- **Tool system — typed Action/Executor/Observation.** Define `Action` / `Observation` (Pydantic) +
  `ToolExecutor[A,O]` + `ToolDefinition[A,O]` with a `create(cls, conv_state, **params)` classmethod,
  then `register_tool("Name", Tool)`. Restrict the tool set by listing tools on the `Agent`. Custom
  tools are genuinely easy.
- **MCP — first-class.** `MCPToolDefinition` / `MCPToolExecutor` wrap FastMCP's client; MCP JSON
  Schemas auto-translate into `Action` models. Config via `AgentSettings.mcp_config`
  (`.mcp.json`-shaped).
- **Skills / microagents — the user-extension point.** `Skill(name=..., content=...,
  trigger=KeywordTrigger(keywords=[...]))`; `trigger=None` = always-on (augments system prompt).
  Loadable from markdown: `.openhands/skills/`, plus compatible `.cursorrules` / `agents.md`.
  Sub-agent delegation via a delegation tool (sub-agents inherit parent model + workspace).
- **Context management — condensers.** `Condenser` drops old events and replaces them with summaries
  (`CondensationEvent` recorded in the log); default `LLMSummarizingCondenser`. Summarization-based;
  knowledge grounding is via Skills (no built-in RAG).
- **Security / human-in-the-loop — built-in maker-checker.** `SecurityAnalyzer` rates each tool call
  low/medium/high/unknown (`LLMSecurityAnalyzer` appends a `security_risk` field). `ConfirmationPolicy`
  + `confirmation_mode` decide whether approval is required; when it is, the agent enters
  `WAITING_FOR_CONFIRMATION` until explicit approve/reject — gate-before-execution interception. An
  `on_event(event) -> None` callback emits every event, so any observer can audit each action.
  `SecretRegistry` masks secrets as `<secret-hidden>`.
- **Workspace — process/exec-env separation is first-class.** `BaseWorkspace`: `execute_command()`,
  `file_upload()`, `file_download()`. `LocalWorkspace` (in-process subprocess), `DockerWorkspace`,
  `RemoteWorkspace` / `RemoteAPIWorkspace` (same interface over HTTP to an agent server). The agent
  loop can run in one place while actions execute elsewhere via the identical interface — maps cleanly
  onto VM-as-cage.
- **Agent server.** `python -m openhands.agent_server` (FastAPI). REST `POST/GET /conversations`;
  WebSocket `/conversations/{id}/events/socket` streams structured events. `RemoteConversation`
  serializes agent config → server reconstructs, runs the loop, and streams events back. Official
  Docker images bundle VSCode-Web + VNC + Chromium (heavy; optional — not needed to embed the SDK).
- **Packaging realities.** Core install is light Python application code (Python ≥ 3.12) but *not
  strictly native-dep-free* — `pillow` / `fakeredis[lua]` carry compiled components (shipped as
  prebuilt wheels, so a minimal Linux VM with read-only Python + writable HOME/TMP still works,
  provided wheels exist for the guest's target arch). Headless without the browser is supported:
  browser weight lives in `openhands-tools` (`browser-use` → Playwright/Chromium). Install
  `openhands-sdk` plus only the tools you need (Terminal/FileEditor/Grep), skip the browser, stay
  slim. *Caveat:* `openhands-workspace` depends on `openhands-agent-server`, so pulling
  `LocalWorkspace` from that package also drags in FastAPI/uvicorn — check whether the cage can run
  the loop without it (or accept the server dependency).

---

## 3. Atelier current state — integration anchors

The seams an engine swap must satisfy (confirmed `file:line`):

### NDJSON serve protocol — `packages/artisan/src/cli-guest.ts`

- **Wire types** (`:18-33`)
  - stdin (host → agent): `{"type":"user","text"}`, `{"type":"export_context"}`, `{"type":"close"}`
  - stdout (agent → host): `init{sessionId}`, `text{text}`, `tool_use{id,name,input}`,
    `tool_result{id,content,isError}`, `policy{door,action,decision,reason,detail}`,
    `result{subtype,result}`, `turn_done`, `context{sessionId,transcript}`, `error{message}`
- **CLI flags** (`:51-62`): `--workspace` (default `/workspace`), `--model`, `--max-turns`
  (default 20), `--task` (one-shot), `--serve` (persistent), `--resume <id>`
- **Provider consumed** (`:128`): `resolveProvider({ model: values.model })`
- **Policy gate wired** (`:195-199`): `canUseTool` → `policy.evaluate(toolName, input)` → allow/deny

### Policy / audit seam — `packages/artisan/src/seams/policy.ts`

- `Decision { behavior: "allow"|"deny"; reason }` (`:22-25`)
- `AuditEntry { door: "files"|"compute"|"network"|"other"; action; decision; reason; path? }`
  (`:29-35`)
- `GUEST_ALLOW` (`:55-70`): Bash, BashOutput, KillShell/KillBash, Read, Write, Edit, MultiEdit,
  NotebookEdit, Glob, Grep, LS, TodoWrite, ExitPlanMode
- `GUEST_DENY` (`:74`): WebFetch, WebSearch
- Unknown tool → deny (`:140-148`)
- *Note:* today it is binary allow/deny — there is no "ask" path. OpenHands' `ConfirmationPolicy`
  fills this gap.

### Provider seam — `packages/provider/src/index.ts`

- `ProviderConfig { model: string; env: Record<string,string> }` (`:4-9`)
- Model order (`:31`): `opts.model` → `ATELIER_MODEL` → `"claude-sonnet-4-6"`
- Env (`:23,35`): `ANTHROPIC_API_KEY` (required; throws if missing), `ANTHROPIC_BASE_URL` (optional)
- This Anthropic-only resolver is what LiteLLM replaces for goal 1.

### Session Manager — `apps/desktop/src/main/sessions/manager.ts`

- Path constants (`:51-53`): `GUEST_TSX=/opt/atelier/packages/artisan/node_modules/.bin/tsx`,
  `GUEST_CWD=/opt/atelier/packages/artisan`, `GUEST_AGENT=src/cli-guest.ts`
- Launch (`:358-360`): `tsx src/cli-guest.ts --serve --workspace <guestPath> [--resume <id>]`
- Injected env (`:361-377`): `ANTHROPIC_API_KEY`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`,
  `DISABLE_AUTOUPDATER/TELEMETRY/ERROR_REPORTING=1`, `HOME=/home/atelier`, `TMPDIR=/tmp`,
  `XDG_CACHE_HOME=/home/atelier/.cache`, plus optional `ATELIER_MODEL`, `ANTHROPIC_BASE_URL`
- Ready signal (`:340-354`): `node --version` exit 0. Turn done (`:436`): `ev.type==="turn_done"`
- Egress (`:337`): `setEgressPolicy(this.egressAllow)`; default `["api.anthropic.com"]` (`:107`)
- Persistence — `store.ts:12-22`: `StoredSession { appId, folder, title, sdkSessionId, transcript[],
  status, updatedAt }`, file `work-sessions.json` in userData

### One-shot path — `services/cmd/atelierctl/main.go`

- Egress (`:183-197`): default `["api.anthropic.com"]`; clock sync `setTime` (`:199-206`)
- Exec (`:232-238`): `tsx src/cli-guest.ts --task <task>` in `GUEST_CWD`; env (`:208-226`) mirrors
  the Session Manager set

### Guest packaging — `image/`

- `image/agent/Dockerfile`: base `ubuntu:24.04`, Node.js 22.x via NodeSource (`:23`), copies
  `packages/` to `/opt/atelier/packages` (`:29`), `npm ci --omit=dev` in `packages/artisan` (`:30-32`)
- `image/build.sh`: `stage_agent_ctx` (`:56-66`) stages `packages/{agent,provider,protocol}`;
  `cmd_runner` (`:252-308`) cross-compiles runner, builds the agent image, and packs both into ext4
- `image/guest/init.sh:79`: mounts the runner volume read-only at `/opt`
  (`mount -t ext4 -o ro -L runner /opt`), then `exec /opt/runner/atelier-runner` (`:81`)
- Agent engines pin: `"node": ">=22.12.0"`

### Egress jail — `services/internal/netjail/*`, `services/internal/broker/network.go`

- `SetEgressPolicyParams { Allow []string }` — host suffixes; empty = deny all
  (`broker/network.go:18-34`)
- `netjail/filter.go`: default `NewAllowlist` empty = deny all (`:46`); `Resolve` only resolves
  allowlisted names, else NXDOMAIN (`:106-124`); `AllowIP` permits only recently-pinned IPs
  (`:129-136`); pin TTL 5 min (`:21`)
- `netjail/network.go`: TCP forwarder checks `AllowIP` before dial, else RST (`:187-211`); DNS via
  `pinResolver` (`:219-243`); no UDP/ICMP forwarders
- runner runs `gvforwarder` over vsock (`cmd/runner/egress_linux.go:27-54`)
- For goal 1, the default allowlist must become per-provider (today hardcoded `api.anthropic.com`).

---

## 4. Module verdicts — use / adapt / skip

| OpenHands module | Verdict | Maps to (Atelier) | Goal |
|---|---|---|---|
| `LLM` + LiteLLM + `RouterLLM` | **Use** | replaces `packages/provider` (`index.ts:31`) | 1 |
| `Conversation` + `EventLog` + `persistence_dir` replay | **Use** | replaces `store.ts` transcript + `--resume` | 2 |
| `Agent` (immutable spec) | **Use** | the agent definition | 2 |
| Tool system (`Action` / `Observation` / `ToolExecutor`) | **Use** | typed domain tools / calculators | 2, 3 |
| `TerminalTool` + `FileEditorTool` + `GrepTool` | **Use** (now) | replaces claude-agent-sdk coding tools | 2 |
| MCP via FastMCP | **Use** (later) | domain connectors (fund system, custodian, AML) | 3 |
| `SecurityAnalyzer` + `ConfirmationPolicy` + `WAITING_FOR_CONFIRMATION` + `on_event` | **Use** | upgrades `policy.ts` allow/deny → allow/ask/deny + audit | 2, 3 |
| `Skill` / microagents | **Use** (later) | user-defined custom skills | 3 |
| `Workspace` (`LocalWorkspace` in-cage) | **Use** | the VM cage / Topology B | 2 |
| `SecretRegistry` | **Use** | clean audit logs (keys never logged) | 2 |
| `agent-server` (FastAPI + WS) | **Adapt** | becomes the in-guest serve transport, or unused behind NDJSON | 2, 3 |
| `Condenser` | **Adapt** | long-session context management; optional at first | 3 |
| `TaskTrackerTool` + sub-agent delegation | **Adapt** | the tasks / process expansion | 3 |
| `browser-use` / Playwright / Chromium | **Skip** | heavy, not needed | — |
| VNC + VSCode-Web Docker image | **Skip** | the cage is our isolation | — |

### Priority order

1. **LiteLLM provider seam** (goal 1) — one change unlocks Anthropic + OpenAI + Bedrock + Azure +
   local Ollama/vLLM (data residency). Requires a per-provider egress allowlist.
2. **Event-sourced `Conversation` + persistence/replay** — *is* the audit trail and resume, for
   free. Persist the event log host-side (outside the cage) via `on_event` so it is tamper-proof.
3. **`ConfirmationPolicy` / `WAITING_FOR_CONFIRMATION`** — fills the missing "ask" path
   (maker-checker control point).
4. **Tool system + MCP** — substrate for all of goal 3.
5. **`Skill` / microagents** — direct answer to "custom skills from the user" (goal 3).

---

## 5. Adoption plan (phased, by goal)

### Phase 1 — goals 1 + 2: like-for-like swap behind the NDJSON seam

Replace the TypeScript claude-agent-sdk agent with a Python OpenHands agent, keeping the existing
NDJSON serve contract so the host (Session Manager + atelierctl) is unchanged on the wire.

- **Engine:** `Agent(llm=LLM(...), tools=[TerminalTool, FileEditorTool, GrepTool])` + `Conversation`
  with `persistence_dir` for resume.
- **Provider-agnostic (goal 1):** `LLM` via LiteLLM replaces `resolveProvider`. Generalize the env
  contract (`ANTHROPIC_API_KEY` / `ANTHROPIC_BASE_URL` → `LLM_API_KEY` / `LLM_MODEL` / `LLM_BASE_URL`,
  with back-compat mapping). Make the egress allowlist per-provider (no longer hardcoded
  `api.anthropic.com`).
- **Keep WORK working (goal 2):** Terminal/FileEditor/Grep give the same coding hands the current
  WORK feature has today.
- **Adapters to write:**
  - `on_event(event)` → NDJSON emitter (see the mapping table in §6).
  - host stdin (`user` / `export_context` / `close`) → `Conversation.send_message()` / state export /
    shutdown.
  - `--resume <id>` → `Conversation(persistence_dir=..., conversation_id=id)` replay.
  - policy bridge: `policy.ts` allow/deny plus the new "ask" → `ConfirmationPolicy` +
    `SecurityAnalyzer`; keep emitting `policy` NDJSON events for the host audit stream.
- **Packaging:** add Python 3.12 + `openhands-sdk` (plus Terminal/FileEditor/Grep tools, no browser)
  to the guest image; pack on the read-only `/opt` volume with writable `HOME` / `TMP` / cache. Update
  `image/agent/Dockerfile` and the runner-volume build.

**Outcome:** WORK keeps working; Claude lock-in is gone. Low conceptual risk — a straight
substitution behind a seam we already own.

### Phase 2 — goal 3: expand

- **MCP connectors** — domain systems (fund accounting platform, custodian, market data, AML/KYC)
  as MCP servers via `mcp_config`.
- **Custom skills from the user** — `Skill` / microagents authored as markdown (keyword-triggered or
  always-on); a user-facing skills folder synced into the agent context.
- **Tasks / process** — `TaskTrackerTool` + sub-agent delegation; `RouterLLM` to route cheap/local
  vs. frontier per step; condensers for long sessions.
- **Domain tools + maker-checker** — deterministic typed calculators/validators (the LLM never does
  arithmetic) + `ConfirmationPolicy` gating the few irreversible business actions.

---

## 6. `on_event` → NDJSON mapping (Phase 1 adapter contract)

| OpenHands event / state | NDJSON out (`cli-guest.ts:18-33`) | Notes |
|---|---|---|
| conversation created / `conversation_id` | `init{sessionId}` | sessionId = OpenHands `conversation_id` |
| `MessageEvent` (assistant text) | `text{text}` | streamed assistant output |
| `ActionEvent` (tool call) | `tool_use{id,name,input}` | id = action/event id |
| `ObservationEvent` | `tool_result{id,content,isError}` | isError from observation error flag |
| `SecurityAnalyzer` / `ConfirmationPolicy` decision | `policy{door,action,decision,reason,detail}` | decision ∈ allow/ask/deny; door mapped from tool |
| final agent result | `result{subtype,result}` | end-of-run |
| agent idle / run complete | `turn_done` | host turn-done signal (`manager.ts:436`) |
| state export request | `context{sessionId,transcript}` | from `export_context` stdin |
| `AgentErrorEvent` / exception | `error{message}` | |

Host → agent stdin: `user{text}` → `send_message()`; `export_context` → serialize state →
`context{...}`; `close` → graceful shutdown.

**New for the "ask" path:** add a host → agent stdin message to approve/reject a pending action when
the agent is in `WAITING_FOR_CONFIRMATION` (e.g. `{"type":"confirm","id","approve":bool}`), and an
agent → host event to announce the pending action. This is the one genuine protocol extension Phase 1
introduces.

---

## 7. Costs and risks

- **Python 3.12 in the guest.** Today the guest is Node-22-only (`agent/Dockerfile:23`,
  `init.sh:79`). OpenHands adds a Python runtime plus its dependencies on the read-only `/opt` volume.
  Feasible (headless, no browser), but mind the footprint: the dependency tree is larger than the core
  five and is not native-dep-free (`pillow`, `fakeredis[lua]`) — it must install for the guest's
  target arch (`linux/arm64` on macOS, `linux/amd64` on Windows), mirroring how the Node agent already
  pins arch. Real changes to `image/agent/Dockerfile` and the runner-volume build, and the in-guest
  agent is rewritten in Python.
- **Two runtimes or one?** runner is Go (unaffected). The agent moves Node → Python. Decide whether
  to drop Node from the guest entirely (if nothing else needs `tsx`) or keep both during transition.
- **Adapter surface.** The `on_event` → NDJSON bridge, the stdin → Conversation bridge, and the
  policy → ConfirmationPolicy bridge are net-new code we own.
- **Dependency churn.** OpenHands V1 is young (core ~v1.2x as of 2026-05). Keeping it behind the
  NDJSON seam contains the blast radius if its API shifts.
- **Egress.** Per-provider allowlist + DNS-pin behaviour (`netjail/filter.go`) must be validated for
  each new provider endpoint (including local models on the host — may need a host-loopback
  allowance).

---

## 8. What stays ours (lock-in prevention)

Three things are deliberately ours, not OpenHands', so the boundary — not the brand — prevents
lock-in:

1. The **NDJSON serve protocol** (the engine seam).
2. The **host-side audit/event log** and **egress/provider policy** (containment & compliance).
3. The **domain tool + skill vocabulary** (Phase 2) the operations agents are built from.

OpenHands is an implementation detail behind these — swappable the same way we want the LLM provider
to be.

---

## 9. Open decisions (resolve before the Phase 1 build)

- **Transport:** keep NDJSON (recommended — lowest blast radius) vs. run `openhands-agent-server`
  in-guest and have the host speak its WebSocket event API directly.
- **Coexist vs. replace:** ship the Python engine alongside the TS agent behind a flag during
  transition, or cut over.
- **Env contract:** adopt `LLM_*` natively vs. keep `ATELIER_MODEL` / `ANTHROPIC_*` and map
  internally.
- **Local-model networking:** how local/in-tenant models reach the host through the egress jail.
