"""partisan — Atelier's in-guest AI agent (Python, OpenHands SDK).

Phase 1: one-shot ``--task`` mode.
Phase 2: persistent ``--serve`` loop with ``--resume <id>`` for hibernate→resume,
         token streaming, and mid-LLM-call interrupt (async ``arun()``).
"""

from __future__ import annotations

import os

# The SDK prints a banner at import time and LiteLLM logs to stderr; silence the
# banner before importing openhands so nothing competes with the NDJSON stream.
os.environ.setdefault("OPENHANDS_SUPPRESS_BANNER", "1")

import argparse
import asyncio
import contextlib
import json
import sys
import threading
import uuid
from pathlib import Path

# stdout carries ONLY NDJSON. Redirect any stray library prints to stderr so a
# rogue print() inside a dependency can never corrupt the wire.
_NDJSON_OUT = sys.stdout
sys.stdout = sys.stderr

from pydantic import SecretStr

from openhands.sdk import (
    LLM,
    Agent,
    Conversation,
    ConversationExecutionStatus,
    Event,
    Tool,
)
from openhands.sdk.conversation import ConversationState
from openhands.sdk.event import (
    ActionEvent,
    AgentErrorEvent,
    InterruptEvent,
    MessageEvent,
    ObservationBaseEvent,
    ObservationEvent,
    PauseEvent,
)
from openhands.sdk.event.conversation_error import ConversationErrorEvent
from openhands.sdk.llm import content_to_str
from openhands.tools.file_editor import FileEditorTool
from openhands.tools.grep import GrepTool
from openhands.tools.terminal import TerminalTool


DEFAULT_MODEL = "anthropic/claude-sonnet-4-6"

# OpenHands tool name -> Atelier "door" (mirrors artisan's doorFor in
# packages/artisan/src/seams/policy.ts). Anything unmapped is "other".
_DOOR = {"terminal": "compute", "file_editor": "files", "grep": "files"}

_RENDERABLE = {"text", "tool_use", "tool_result", "policy", "result"}

_RESUME_RECOVERY = (
    "A restart occurred while a tool was in progress; the tool execution was "
    "interrupted and did not complete."
)

# emit() may be called from the asyncio loop thread (event/token callbacks,
# control loop) and — defensively — from any thread a future SDK callback might
# use. One lock keeps each NDJSON line atomic so concurrent writers can't
# interleave bytes on the wire.
_emit_lock = threading.Lock()


def emit(obj: dict) -> None:
    line = json.dumps(obj) + "\n"
    with _emit_lock:
        _NDJSON_OUT.write(line)
        _NDJSON_OUT.flush()


def resolve_model(cli_model: str | None) -> str:
    model = cli_model or os.getenv("LLM_MODEL") or os.getenv("ATELIER_MODEL") or DEFAULT_MODEL
    # The "openhands/" prefix routes through the All-Hands proxy — an egress leak
    # that bypasses our per-provider jail. Never allow it.
    if model.startswith("openhands/"):
        raise SystemExit(
            "partisan: refusing 'openhands/' model prefix (routes to the All-Hands proxy)"
        )
    if "/" not in model:
        model = f"anthropic/{model}"
    return model


def resolve_api_key() -> SecretStr:
    key = os.getenv("LLM_API_KEY") or os.getenv("ANTHROPIC_API_KEY")
    if not key:
        raise SystemExit("partisan: no API key (set LLM_API_KEY or ANTHROPIC_API_KEY)")
    return SecretStr(key)


def resolve_base_url() -> str | None:
    return os.getenv("LLM_BASE_URL") or os.getenv("ANTHROPIC_BASE_URL") or None


def resolve_persistence_dir() -> Path:
    # The SDK appends ``<conversation_id>.hex`` itself (see
    # BaseConversation.get_persistence_dir), so return the flat base.
    base = os.environ.get("PARTISAN_PERSIST", str(Path.home() / ".partisan"))
    return Path(base)


def on_token(chunk) -> None:
    """token_callbacks: stream assistant text deltas as they arrive.

    Mirrors agent-server's delta forwarding (event_service.py): read
    ``chunk.choices[].delta.content``. Deltas are a transient UX affordance —
    the authoritative full text still arrives as a final ``text`` event from the
    MessageEvent — so they are not added to the transcript.
    """
    for choice in getattr(chunk, "choices", None) or ():
        delta = getattr(choice, "delta", None)
        if delta is None:
            continue
        content = getattr(delta, "content", None)
        if isinstance(content, str) and content:
            emit({"type": "text_delta", "text": content})


def make_on_event(state: dict, transcript: list | None = None):
    """Build the SDK callback that maps each Event to an NDJSON line.

    When ``transcript`` is given (serve mode), RENDERABLE events are also
    appended to it so they can be exported with ``export_context``.
    """

    def on_event(event: Event) -> None:
        obj: dict | None = None
        if isinstance(event, MessageEvent):
            if event.source != "agent":
                return
            text = "".join(content_to_str(event.llm_message.content))
            if not text:
                return
            state["last_text"] = text
            obj = {"type": "text", "text": text}
        elif isinstance(event, ActionEvent):
            tool_input = event.action.model_dump(mode="json") if event.action is not None else {}
            tool_use = {
                "type": "tool_use",
                "id": event.tool_call_id,
                "name": event.tool_name,
                "input": tool_input,
            }
            emit(tool_use)
            if transcript is not None:
                transcript.append(tool_use)
            obj = {
                "type": "policy",
                "door": _DOOR.get(event.tool_name, "other"),
                "action": event.tool_name,
                "decision": "allow",
                "reason": "in-cage tool permitted by partisan policy",
            }
        elif isinstance(event, ObservationEvent):
            content = "".join(content_to_str(event.observation.to_llm_content))
            obj = {
                "type": "tool_result",
                "id": event.tool_call_id,
                "content": content,
                "isError": event.observation.is_error,
            }
        elif isinstance(event, AgentErrorEvent):
            obj = {"type": "error", "message": event.error}
        elif isinstance(event, ConversationErrorEvent):
            # Run-loop failure delivered via callback. The exception path also
            # re-raises (handled by the run wrapper), but the max-iterations cap
            # only fires this callback and returns normally — so record it and
            # let the post-run check turn the turn's result into an error.
            state["error"] = f"{event.code}: {event.detail}"
            return
        elif isinstance(event, (InterruptEvent, PauseEvent)):
            # arun() emits InterruptEvent after a mid-LLM-call cancel (and has
            # already patched any orphaned tool call); pause() emits PauseEvent.
            state["interrupted"] = True
            obj = {"type": "interrupted"}

        if obj is None:
            return
        if transcript is not None and obj.get("type") in _RENDERABLE:
            transcript.append(obj)
        emit(obj)

    return on_event


def build_conversation(
    args,
    on_event,
    conversation_id: uuid.UUID,
    persistence_dir: Path | None,
) -> Conversation:
    llm = LLM(
        usage_id="agent",
        model=resolve_model(args.model),
        api_key=resolve_api_key(),
        base_url=resolve_base_url(),
        stream=True,  # enables token_callbacks (text_delta streaming)
    )
    agent = Agent(
        llm=llm,
        tools=[
            Tool(name=TerminalTool.name),
            Tool(name=FileEditorTool.name),
            Tool(name=GrepTool.name),
        ],
    )
    kwargs: dict = dict(
        agent=agent,
        callbacks=[on_event],
        token_callbacks=[on_token],
        workspace=args.workspace,
        conversation_id=conversation_id,
    )
    if persistence_dir is not None:
        kwargs["persistence_dir"] = str(persistence_dir)
    return Conversation(**kwargs)


def recover_unmatched_actions(conversation) -> None:
    """On resume, surface a tool call that was in-flight at kill time.

    A conversation persisted with RUNNING status was killed mid-run, so its
    replayed history may hold an ActionEvent with no matching observation —
    which provider APIs reject on the next completion. Inject a synthetic error
    observation so the history stays valid. Mirrors agent-server's start()
    recovery (event_service.py).
    """
    state = conversation.state
    if state.execution_status != ConversationExecutionStatus.RUNNING:
        return
    state.execution_status = ConversationExecutionStatus.ERROR
    unmatched = ConversationState.get_unmatched_actions(state.events)
    if not unmatched:
        return
    first = unmatched[0]
    already_observed = any(
        isinstance(e, ObservationBaseEvent) and e.tool_call_id == first.tool_call_id
        for e in state.events
    )
    if already_observed:
        return
    conversation._on_event(
        AgentErrorEvent(
            tool_name=first.tool_name,
            tool_call_id=first.tool_call_id,
            error=_RESUME_RECOVERY,
        )
    )


def run_once(args) -> int:
    state: dict = {"last_text": "", "error": None}
    conversation_id = uuid.uuid4()
    # Build first: a missing key / bad model fails fast before we emit anything.
    conversation = build_conversation(args, make_on_event(state), conversation_id, None)
    emit({"type": "init", "sessionId": str(conversation_id)})
    try:
        try:
            conversation.send_message(args.task)
            conversation.run()
        except Exception as exc:  # surface any run failure on the wire, then exit non-zero
            emit({"type": "error", "message": f"{type(exc).__name__}: {exc}"})
            emit({"type": "result", "subtype": "error_during_execution", "result": ""})
            return 1
        # The max-iterations cap ends run() via a ConversationErrorEvent callback
        # without raising, so check for it before declaring success.
        if state["error"]:
            emit({"type": "error", "message": state["error"]})
            emit({"type": "result", "subtype": "error_during_execution", "result": ""})
            return 1
        emit({"type": "result", "subtype": "success", "result": state["last_text"]})
        return 0
    finally:
        conversation.close()


async def run_serve(args) -> int:
    """Multi-turn NDJSON serve loop with resume, streaming, and interrupt.

    An asyncio control loop drives turns via ``conversation.arun()`` launched as
    a background task, so a ``{"type":"interrupt"}`` control message can cancel
    an in-flight LLM call mid-completion (mirrors agent-server's run model).
    stdin is read on a daemon thread that hands control messages to the loop via
    ``call_soon_threadsafe``; all NDJSON is emitted on the loop thread.
    """
    conversation_id = uuid.UUID(args.resume) if args.resume else uuid.uuid4()
    persistence_dir = resolve_persistence_dir()
    persistence_dir.mkdir(parents=True, exist_ok=True)

    transcript: list[dict] = []
    state: dict = {"last_text": None, "error": None, "interrupted": False}

    conversation = build_conversation(
        args, make_on_event(state, transcript), conversation_id, persistence_dir
    )
    if args.resume:
        recover_unmatched_actions(conversation)

    emit({"type": "init", "sessionId": str(conversation_id)})

    loop = asyncio.get_running_loop()
    ctrl: asyncio.Queue = asyncio.Queue()

    def _reader() -> None:
        for line in sys.stdin:
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                msg = {"type": "_parse_error"}
            loop.call_soon_threadsafe(ctrl.put_nowait, msg)
        loop.call_soon_threadsafe(ctrl.put_nowait, None)  # EOF sentinel

    threading.Thread(target=_reader, daemon=True).start()

    current_run: asyncio.Task | None = None

    async def do_turn(text: str) -> None:
        state["last_text"] = None
        state["error"] = None
        state["interrupted"] = False
        try:
            conversation.send_message(text)
            # arun() swallows CancelledError from interrupt() (sets PAUSED and
            # emits InterruptEvent), so a mid-LLM cancel returns normally here;
            # only genuine failures (ConversationRunError, …) raise.
            await conversation.arun()
        except Exception as exc:
            emit({"type": "error", "message": f"{type(exc).__name__}: {exc}"})
            result_obj = {"type": "result", "subtype": "error_during_execution", "result": ""}
            transcript.append(result_obj)
            emit(result_obj)
            emit({"type": "turn_done"})
            return

        if state["error"]:
            emit({"type": "error", "message": state["error"]})
            result_obj = {"type": "result", "subtype": "error_during_execution", "result": ""}
        elif state["interrupted"]:
            result_obj = {"type": "result", "subtype": "interrupted", "result": state.get("last_text") or ""}
        else:
            result_obj = {"type": "result", "subtype": "success", "result": state.get("last_text") or ""}
        transcript.append(result_obj)
        emit(result_obj)
        emit({"type": "turn_done"})

    async def drain_run() -> None:
        nonlocal current_run
        if current_run is not None and not current_run.done():
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await current_run
        current_run = None

    try:
        while True:
            msg = await ctrl.get()
            if msg is None:
                break

            t = msg.get("type", "")

            if t == "_parse_error":
                emit({"type": "error", "message": "stdin: invalid JSON line"})
                continue

            if t == "user":
                if current_run is not None and not current_run.done():
                    emit({"type": "error", "message": "busy: a turn is already running"})
                    continue
                current_run = asyncio.create_task(do_turn(str(msg.get("text", ""))))

            elif t in ("interrupt", "pause"):
                # Cancel the in-flight arun() task mid-LLM-call. interrupt() is
                # a no-op (falls back to pause) when nothing is running.
                if current_run is not None and not current_run.done():
                    conversation.interrupt()

            elif t == "export_context":
                # Finish any in-flight turn before snapshotting (mirror
                # cli-guest.ts: defer export until idle).
                await drain_run()
                emit({"type": "context", "sessionId": str(conversation_id), "transcript": transcript})
                break

            elif t == "close":
                if current_run is not None and not current_run.done():
                    conversation.interrupt()
                    await drain_run()
                break

            else:
                emit({"type": "error", "message": f'stdin: unknown control "{t}"'})

    finally:
        if current_run is not None and not current_run.done():
            conversation.interrupt()
            await drain_run()
        conversation.close()

    return 0


def parse_args(argv):
    p = argparse.ArgumentParser(prog="partisan", description="Atelier in-guest agent (OpenHands)")
    p.add_argument("--workspace", default="/workspace")
    p.add_argument("--model", default=None)
    p.add_argument("--max-turns", default="20")  # accepted for flag parity
    p.add_argument("--task", default=None)
    p.add_argument("--serve", action="store_true")
    p.add_argument("--resume", default=None)
    return p.parse_args(argv)


def main(argv=None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    if args.serve or args.resume:
        return asyncio.run(run_serve(args))
    if not args.task:
        raise SystemExit("partisan: nothing to do (pass --task or --serve)")
    return run_once(args)


if __name__ == "__main__":
    sys.exit(main())
