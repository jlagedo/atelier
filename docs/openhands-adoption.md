# partisan — OpenHands adoption plan

| Field | Detail |
|---|---|
| Status | **Phase 1 done** (commit `28df76c`) — one-shot `--task` over NDJSON, verified live on a real model call. Phase 2 (serve/resume) next. |
| Project | **partisan** — Python (OpenHands SDK) successor to artisan, behind the same NDJSON wire |
| Goal | Replace the Anthropic-locked in-guest agent with a provider-agnostic one (LiteLLM) |
| Approach | Embed the SDK **in-process** (`LocalConversation` + `callbacks`); NDJSON only at the host↔guest edge |
| Validated | SDK cloned to `~/Developer/software-agent-sdk`, read against source; 410 MB minimal install |

artisan (TypeScript, `@anthropic-ai/claude-agent-sdk`) targets Anthropic only. **partisan** rebuilds the
in-guest agent on the OpenHands SDK (Python, `OpenHands/software-agent-sdk` @ v1.23.0, MIT, Python ≥3.12)
for model-provider freedom, keeping Atelier's NDJSON serve wire so the host (Session Manager, atelierctl)
is unchanged. Scope is the live in-guest path (`cli-guest.ts`, Topology B); the host-loop `cli.ts` is out
of scope.

---

## 1. Decisions

| # | Decision | Why |
|---|---|---|
| D1 | Build **partisan** on the OpenHands SDK as artisan's successor. | Provider freedom; the objection is *model* lock-in, not SDK use. |
| D2 | Embed in-process (`LocalConversation` + `callbacks`); **no** `agent-server`. **This is the one and only place we deviate from stock OpenHands.** | We run a **local VM as the cage**, not a Docker/remote deployment — so the transport is the VM's vsock pipe and agent-server's REST/WS/webhook fan-out is dead weight. We tap `callbacks=` directly instead (§2). |
| D3 | Keep the **NDJSON wire** (`cli-guest.ts:18-33`); translate SDK events ↔ NDJSON at the process edge. | Lowest blast radius; rides the existing audited `exec` door. |
| D4 | **Coexist** via a **hardwired launch site** (no env switch); switching = edit + rebuild. | No runtime selector to build then delete; A/B is the conformance suite, not runtime. |
| D5 | **Cutover when green** (conformance suite + `e2e:host` on the Python path) → flip launch, drop Node, guest Python-only. | A named exit prevents two-runtime limbo. |
| D6 | Post-cutover, artisan + `packages/provider` stay as **reference source** (unbuilt). | The TS path can't run without Node anyway. |
| D7 | **API key the OpenHands way:** `LLM(api_key=SecretStr(env))`, never on the wire, redacted in persistence, re-injected each launch. | Containment + egress jail are the real control; you can't hide a key from the process using it (§2). |
| D8 | **Build first, trim later** — install `openhands-sdk`+`openhands-tools` as-is (410 MB; browser import-safe); size matters, but it's a post-cutover concern, not Phase-1 work. | Don't let footprint slow the build; revisit once partisan is green — and trim by dropping deps, *not* by forking/vendoring SDK code (D9). |
| D9 | **Commit to OpenHands; no wrapper layer around its API.** Use SDK types directly; copy its behavior when in doubt; the **only** adapters are at the Atelier boundary (NDJSON wire, egress, key resolver) — never around the SDK. Accept partisan isn't framework-swappable. | We want **model** freedom (LiteLLM), not **framework** freedom. The replaceability tax > the lock-in it insures against (cf. abstracting Oracle to stay DB-agnostic); churn is contained by pinned versions + conformance, not abstraction. |

**Parked:** OIDC / per-user-token auth + a company LLM-gateway `base_url` (reduces key risk, fits data
residency, but adds mid-session token refresh). The rule it leaves today: never use the
`openhands/<model>` prefix — it routes to All-Hands' proxy (`llm.py:502-508`).

---

## 2. OpenHands — what we use

**Packages.** Monorepo of four; take **two** — `openhands-sdk` (core: `LLM`, `Agent`, `Conversation`,
`Tool`, events, MCP, security, condensers) + `openhands-tools` (`terminal`, `file_editor`, `grep`, …).
Skip `openhands-workspace` (pulls agent-server) and `openhands-agent-server`.

**The in-process seam.** `LLM(model, api_key, base_url)` (LiteLLM, 100+ providers) → `Agent(llm, tools=[…])`
→ `Conversation(agent, workspace=<path>, persistence_dir, conversation_id, callbacks=[fn])` →
`send_message()` + `run()`. `callbacks=[fn]` (`fn(event: Event)`) **is** the whole event stream —
in-process, no wire. agent-server's `PubSub`/WS/webhooks merely fan that one callback out to many
networked subscribers (`event_service.py:669`); we have one consumer over one pipe, so we tap `callbacks`
directly (~70 LOC, not a fork). Revisit agent-server only if the topology stops being a local VM (goes remote / multi-consumer).

**Control = four surfaces** (Phase-1 parity uses only the first two):

| Surface | Mechanism | Blocks? | artisan analog |
|---|---|---|---|
| Tool list | `Agent(tools=[…])` — omit ⇒ uncallable | structural | `GUEST_DENY` |
| `callbacks=[fn]` | in-process, every `Event` | no (observe) | `emit()` + audit |
| `hook_config` PreToolUse | subprocess, `exit 2` blocks (Claude Code contract) | yes | `canUseTool` deny |
| `security_policy` + `confirmation_mode` | LLM rates `security_risk`; pause to approve | advisory + ask | *new* ask path |

artisan's guest policy (`policy.ts:140-148`) is name-based, so **tool-list (deny-by-omission) + callbacks
(audit)** reproduce it exactly; hooks / confirmation (the "ask" path) are deferred.

**API key & secrets.** `LLM.api_key: SecretStr` (`llm.py:194`); serialization **redacts by default**
(`pydantic_secrets.py:48-79`), so `persistence_dir` never stores the plaintext key, and on load
`"**********"` → `None` (`:103`) ⇒ re-inject from env each launch incl. `--resume` (already artisan's
model, `manager.ts:361-377`). `SecretRegistry` is a *separate* mechanism for **tool** secrets — injects
per-command, masks values `<secret-hidden>` (later audit bonus).

**Resume.** `Conversation(persistence_dir, conversation_id)` auto-detects and replays prior state
(`local_conversation.py:188`); we own `conversation_id` (a uuid) ⇒ emit `init` immediately.

**Packaging facts.** Python ≥3.12 (ubuntu 24.04 ships it). Minimal install **410 MB**; browser is
**import-safe** (importing `terminal`/`file_editor`/`grep` never loads `browser_use`/`playwright`).
`TerminalTool` → `libtmux` ⇒ guest needs a **`tmux`** binary. Native deps (`pillow`, `fakeredis[lua]`,
`tree-sitter`) ship as per-arch wheels. `lmnr` telemetry is in core — keep inert (no `LMNR_*`).
`persistence_dir` must be on a **writable** guest path (HOME/TMP, not the ro `/opt`).

---

## 3. Atelier seams the plan touches (file:line)

- **NDJSON wire** `cli-guest.ts:18-33` — stdin `user`/`export_context`/`close`; stdout
  `init`/`text`/`tool_use`/`tool_result`/`policy`/`result`/`turn_done`/`context`/`error`. Flags `:51-62`.
- **Policy/audit** `policy.ts` — `Decision` `:22-25`; `AuditEntry{door,action,decision,reason,path}`
  `:29-35`; `GUEST_ALLOW` `:55-70`; `GUEST_DENY` `:74`; unknown→deny `:140-148`.
- **Provider** `provider/src/index.ts` — model order `:31`; env `ANTHROPIC_API_KEY`/`_BASE_URL` `:23,35`.
- **Session Manager** `manager.ts` — launch constants `:51-53`; launch `:358-360`; injected env `:361-377`;
  ready signal `:340-354`; turn-done `:436`; egress default `["api.anthropic.com"]` `:107,337`; persistence
  `store.ts:12-22`.
- **One-shot** `atelierctl/main.go` — egress `:183-197`; exec `:232-238`; env `:208-226`.
- **Guest image** `image/agent/Dockerfile` (Node `:23`, copy `:29`, `npm ci` `:30-32`); `build.sh`
  (`stage_agent_ctx :56-66`, `cmd_runner :252-308`); `init.sh:79-81` (mount ro `/opt`, exec runner).
- **Egress jail** `netjail/filter.go` (deny-all default `:46`, pinned-IP `:129-136`), `network.go:187-211`;
  `broker/network.go:18-34`. Hardcoded `api.anthropic.com` must become per-provider.

---

## 4. Event → NDJSON contract

Built from `callbacks=[fn]`; classes in `openhands-sdk/openhands/sdk/event/`:

| OpenHands `Event` | NDJSON | Source |
|---|---|---|
| `MessageEvent` (assistant) | `text{text}` | `message.py:25` |
| `ActionEvent` | `tool_use{id,name,input}` | `action.py:24` (`tool_name:44`, `tool_call_id:45`, `action:40`) |
| `ObservationEvent` | `tool_result{id,content,isError}` | `observation.py:31` |
| `AgentErrorEvent` / `ConversationErrorEvent` | `error{message}` | `observation.py:123` / `conversation_error.py:7` |
| `ConversationStateUpdateEvent` (finished) | `turn_done` | `conversation_state.py:18` |
| (we own `conversation_id`) | `init{sessionId}` | emit immediately |
| (per `ActionEvent`) | `policy{door,action,decision,reason}` | `door` from tool name |

**Tool names** auto-derive (`tool.py:236-241`): `terminal`, `file_editor`, `grep`, `glob` — structurally
unlike artisan (`terminal`=`Bash`; `file_editor` is one tool spanning `Read`/`Write`/`Edit`). **Rule:**
keep OpenHands names; derive `door` (`terminal`→compute, others→files); mark `tool_use.name` /
`policy.action` **non-strict** in conformance. Available later: `StreamingDeltaEvent` (token streaming),
`PauseEvent` / `InterruptEvent`.

---

## 5. Build plan

`packages/partisan/cli_guest.py`, `process.title="atelier-partisan"`. Key handling (D7) is woven through.
Phases are strictly sequential; Phase 1 de-risks the core.

### Phase 1 — one-shot (`--task`) ✅ DONE (commit `28df76c`)
*Exit (met):* `uv run cli_guest.py --task "create hello.txt" --workspace /tmp/ws` emits a well-formed
NDJSON stream (`init`→`tool_use`→`policy`→`tool_result`→`text`→`result`, id-paired) and creates the file.

Built (`packages/partisan/`, uv + Python 3.14):
- `pyproject.toml` (`openhands-sdk`/`openhands-tools` 1.23.*) via `uv add`; `argparse` mirrors artisan flags (`--serve`/`--resume` stubbed → Phase 2).
- **Provider/key resolver:** model `--model`→`LLM_MODEL`→`ATELIER_MODEL`→`anthropic/claude-sonnet-4-6`, add `anthropic/` when unprefixed, reject `openhands/`; key `LLM_API_KEY`→`ANTHROPIC_API_KEY` (`SecretStr`, fail-fast); base_url `LLM_BASE_URL`→`ANTHROPIC_BASE_URL`.
- `Agent(tools=[Terminal, FileEditor, Grep])` (deny-by-omission); `conversation_id=uuid4()`, `persistence_dir` omitted (optional → Phase 2).
- **Emitter** (`on_event`): the §4 mapping; `action.model_dump(mode="json")`; observation flattened via `content_to_str(observation.to_llm_content)`; `policy{}` per `ActionEvent`. **stdout = NDJSON only** (banner suppressed via `OPENHANDS_SUPPRESS_BANNER`, stray prints redirected to stderr).

Resolved from source: `ObservationEvent` has **no** `is_error` (errors are distinct event types ⇒ `isError:false`); `result` = last assistant `MessageEvent` text after `run()` returns; `run()` blocks (no stdout lock needed yet).

### Phase 2 — serve (`--serve`, `--resume`)
*Exit:* multi-turn over NDJSON; `export_context`→`context{}`; relaunch `--resume <id>` continues.

- stdin reader **thread** → control queue; main thread runs turns; **all stdout under one lock**.
- Turn: `user`→`send_message`+`run`→`result`+`turn_done`; defer `export_context` until idle (mirror `cli-guest.ts:174`).
- `conversation_id` from `--resume` else `uuid4()`; `persistence_dir=$PARTISAN_PERSIST/<id>`; auto-resume; emit `init`.
- `transcript[]` from callbacks (RENDERABLE subset, mirror `cli-guest.ts:140`); `export_context`→`context{sessionId,transcript}`→shutdown.
- *Verify:* send-while-processing (example 18 — serialize vs inject); resume auto-`run()` y/n; `persistence_dir` survives the process kill.

### Phase 3 — packaging + the hardwired switch
*Exit:* a full WORK session driven by partisan on a booted VM.

- `Dockerfile`: `apt install python3-venv python3-pip tmux`; stage `packages/partisan`; build a venv at `/opt/atelier/partisan/.venv` (target-arch wheels). Keep Node+artisan (coexist).
- `build.sh`: `stage_agent_ctx` + runner volume carry the partisan venv; `persistence_dir`→`HOME`.
- **Egress per-provider:** replace hardcoded `api.anthropic.com` (`manager.ts:107`, `atelierctl:183-197`) with a model/`base_url`→host resolver.
- **The switch:** `manager.ts:51-53` → python constants; launch `:358-360`→`python cli_guest.py --serve`; env `:361-377`→`LLM_*` (+ suppress banner, lmnr off), drop `CLAUDE_CODE_*`; ready `:340-354` `node`→`python3`; same for `atelierctl:208-238`.
- *Verify:* venv path-portability (same mount path); `tmux`+TerminalTool in-guest; per-arch wheels present.

### Phase 4 — conformance + cutover
*Exit:* conformance suite + `e2e:host` (Python path) green ⇒ Node removed, artisan = reference source.

- **Conformance suite:** fixtures `(flags + stdin) → ordered stdout`; **strict** on `type` / ordering / `door` / invariants (`tool_use` precedes its `tool_result`; `init` first, `turn_done` last); **non-strict** on ids / free text / names. Live models are non-deterministic ⇒ **structural-invariant checking** (run live a few times) + a partisan-only golden for the emitter — *not* exact transcript equality.
- Python-path mode in `scripts/e2e-host.mjs`.
- Cutover: drop Node + artisan `npm ci` from `Dockerfile`/`build.sh`; update CLAUDE.md/README to "Python-only guest".

### Deferred
**Image trim** (drop unused deps / slim wheels — *not* by vendoring or forking SDK code). Further
OpenHands capabilities: MCP connectors (`mcp_config`); Skills/microagents;
`TaskTrackerTool` + sub-agent delegation + `RouterLLM` + condensers; domain typed tools + the
maker-checker "ask" path (PreToolUse hooks / `confirmation_mode`).

---

## 6. Risks & open calls

- **Conformance is structural, not exact** — live models vary; we assert invariants, not equality (Phase 4).
- **Egress per-provider** — DNS-pin behavior (`netjail/filter.go`) must be validated per new endpoint (local models may need a host-loopback allowance).
- **The adapter surface is ours but lives only at the Atelier boundary** — emitter, stdin bridge, door audit, key/egress resolver: net-new but thin, and they wrap *Atelier's* contracts, not OpenHands' (D9).
- **Verify during build:** run-complete / final-answer signal; `ObservationEvent` shape; send-while-processing; resume auto-run; `lmnr` inert without keys; venv portability.
