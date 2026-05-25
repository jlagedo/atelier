"""partisan — Atelier's in-guest AI agent (Python, OpenHands SDK).

Phase 1: one-shot ``--task`` mode that embeds the OpenHands SDK in-process and
translates its event stream to Atelier's NDJSON wire on stdout. The serve/resume
path and the host launch switch land in later phases.
"""

from __future__ import annotations

import os

# The SDK prints a banner at import time and LiteLLM logs to stderr; silence the
# banner before importing openhands so nothing competes with the NDJSON stream.
os.environ.setdefault("OPENHANDS_SUPPRESS_BANNER", "1")

import argparse
import json
import sys
import uuid

# stdout carries ONLY NDJSON. Redirect any stray library prints to stderr so a
# rogue print() inside a dependency can never corrupt the wire.
_NDJSON_OUT = sys.stdout
sys.stdout = sys.stderr

from pydantic import SecretStr

from openhands.sdk import LLM, Agent, Conversation, Event, Tool
from openhands.sdk.event import (
    ActionEvent,
    AgentErrorEvent,
    MessageEvent,
    ObservationEvent,
)
from openhands.sdk.llm import content_to_str
from openhands.tools.file_editor import FileEditorTool
from openhands.tools.grep import GrepTool
from openhands.tools.terminal import TerminalTool


DEFAULT_MODEL = "anthropic/claude-sonnet-4-6"

# OpenHands tool name -> Atelier "door" (mirrors artisan's doorFor in
# packages/artisan/src/seams/policy.ts). Anything unmapped is "other".
_DOOR = {"terminal": "compute", "file_editor": "files", "grep": "files"}


def emit(obj: dict) -> None:
    _NDJSON_OUT.write(json.dumps(obj) + "\n")
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


def make_on_event(state: dict):
    """Build the SDK callback that maps each Event to an NDJSON line."""

    def on_event(event: Event) -> None:
        if isinstance(event, MessageEvent):
            if event.source != "agent":
                return
            text = "".join(content_to_str(event.llm_message.content))
            if text:
                state["last_text"] = text
                emit({"type": "text", "text": text})
        elif isinstance(event, ActionEvent):
            tool_input = event.action.model_dump(mode="json") if event.action is not None else {}
            emit(
                {
                    "type": "tool_use",
                    "id": event.tool_call_id,
                    "name": event.tool_name,
                    "input": tool_input,
                }
            )
            emit(
                {
                    "type": "policy",
                    "door": _DOOR.get(event.tool_name, "other"),
                    "action": event.tool_name,
                    "decision": "allow",
                    "reason": "in-cage tool permitted by partisan policy",
                }
            )
        elif isinstance(event, ObservationEvent):
            content = "".join(content_to_str(event.observation.to_llm_content))
            emit(
                {
                    "type": "tool_result",
                    "id": event.tool_call_id,
                    "content": content,
                    "isError": False,
                }
            )
        elif isinstance(event, AgentErrorEvent):
            emit({"type": "error", "message": event.error})

    return on_event


def build_conversation(args, on_event):
    llm = LLM(
        usage_id="agent",
        model=resolve_model(args.model),
        api_key=resolve_api_key(),
        base_url=resolve_base_url(),
    )
    agent = Agent(
        llm=llm,
        tools=[
            Tool(name=TerminalTool.name),
            Tool(name=FileEditorTool.name),
            Tool(name=GrepTool.name),
        ],
    )
    conversation_id = uuid.uuid4()
    conversation = Conversation(
        agent=agent,
        callbacks=[on_event],
        workspace=args.workspace,
        conversation_id=conversation_id,
    )
    return conversation, conversation_id


def run_once(args) -> int:
    state = {"last_text": ""}
    # Build first: a missing key / bad model fails fast before we emit anything.
    conversation, conversation_id = build_conversation(args, make_on_event(state))
    emit({"type": "init", "sessionId": str(conversation_id)})
    try:
        conversation.send_message(args.task)
        conversation.run()
    except Exception as exc:  # surface any run failure on the wire, then exit non-zero
        emit({"type": "error", "message": f"{type(exc).__name__}: {exc}"})
        emit({"type": "result", "subtype": "error_during_execution", "result": ""})
        return 1
    emit({"type": "result", "subtype": "success", "result": state["last_text"]})
    return 0


def parse_args(argv):
    p = argparse.ArgumentParser(prog="partisan", description="Atelier in-guest agent (OpenHands)")
    p.add_argument("--workspace", default="/workspace")
    p.add_argument("--model", default=None)
    p.add_argument("--max-turns", default="20")  # accepted for flag parity; unused in Phase 1
    p.add_argument("--task", default=None)
    p.add_argument("--serve", action="store_true")
    p.add_argument("--resume", default=None)
    return p.parse_args(argv)


def main(argv=None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    if args.serve or args.resume:
        raise SystemExit("partisan: --serve/--resume not implemented yet (Phase 2)")
    if not args.task:
        raise SystemExit("partisan: nothing to do (pass --task; --serve is Phase 2)")
    return run_once(args)


if __name__ == "__main__":
    sys.exit(main())
