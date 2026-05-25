"""Layer 1: pure event -> NDJSON mapping (no LLM, no Conversation).

Synthetic SDK events are built with ``model_construct`` so we only set the fields the
mapper reads, sidestepping each event type's many required fields.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

import cli_guest
from openhands.sdk import ConversationExecutionStatus
from openhands.sdk.event import (
    ActionEvent,
    AgentErrorEvent,
    InterruptEvent,
    MessageEvent,
    ObservationEvent,
    PauseEvent,
)
from openhands.sdk.event.conversation_error import ConversationErrorEvent
from openhands.sdk.llm import Message, TextContent


def _msg(text: str, source: str = "agent") -> MessageEvent:
    return MessageEvent.model_construct(
        source=source, llm_message=Message.model_construct(content=[TextContent(text=text)])
    )


def run_mapper(event, transcript=None):
    out: list[dict] = []
    state: dict = {"last_text": None, "error": None, "interrupted": False}
    # emit() is module-global; capture it.
    orig = cli_guest.emit
    cli_guest.emit = out.append
    try:
        cli_guest.make_on_event(state, transcript)(event)
    finally:
        cli_guest.emit = orig
    return out, state


def test_agent_message_emits_text():
    transcript: list = []
    out, state = run_mapper(_msg("hi there"), transcript)
    assert out == [{"type": "text", "text": "hi there"}]
    assert state["last_text"] == "hi there"
    assert transcript == [{"type": "text", "text": "hi there"}]


def test_user_message_is_ignored():
    out, _ = run_mapper(_msg("hello", source="user"))
    assert out == []


def test_empty_agent_message_is_ignored():
    out, _ = run_mapper(_msg(""))
    assert out == []


def test_action_emits_tool_use_then_policy():
    transcript: list = []
    ev = ActionEvent.model_construct(action=None, tool_name="terminal", tool_call_id="c1")
    out, _ = run_mapper(ev, transcript)
    assert out == [
        {"type": "tool_use", "id": "c1", "name": "terminal", "input": {}},
        {
            "type": "policy",
            "door": "compute",
            "action": "terminal",
            "decision": "allow",
            "reason": "in-cage tool permitted by partisan policy",
        },
    ]
    # both renderable -> transcript, in order
    assert [o["type"] for o in transcript] == ["tool_use", "policy"]


@pytest.mark.parametrize(
    "tool,door",
    [("terminal", "compute"), ("file_editor", "files"), ("grep", "files"), ("mystery", "other")],
)
def test_door_mapping(tool, door):
    ev = ActionEvent.model_construct(action=None, tool_name=tool, tool_call_id="c1")
    out, _ = run_mapper(ev)
    assert out[1] == {
        "type": "policy",
        "door": door,
        "action": tool,
        "decision": "allow",
        "reason": "in-cage tool permitted by partisan policy",
    }


def test_observation_maps_is_error():
    obs = SimpleNamespace(to_llm_content=[TextContent(text="out")], is_error=False)
    ev = ObservationEvent.model_construct(observation=obs, tool_call_id="c1")
    out, _ = run_mapper(ev)
    assert out == [{"type": "tool_result", "id": "c1", "content": "out", "isError": False}]

    obs_err = SimpleNamespace(to_llm_content=[TextContent(text="boom")], is_error=True)
    ev2 = ObservationEvent.model_construct(observation=obs_err, tool_call_id="c2")
    out2, _ = run_mapper(ev2)
    assert out2[0]["isError"] is True


def test_agent_error_maps_to_error():
    ev = AgentErrorEvent.model_construct(error="kaboom")
    out, _ = run_mapper(ev)
    assert out == [{"type": "error", "message": "kaboom"}]


def test_conversation_error_records_state_no_emit():
    ev = ConversationErrorEvent.model_construct(code="MaxIterationsReached", detail="too many")
    out, state = run_mapper(ev)
    assert out == []
    assert state["error"] == "MaxIterationsReached: too many"


@pytest.mark.parametrize("cls", [InterruptEvent, PauseEvent])
def test_interrupt_pause_map_to_interrupted(cls):
    out, state = run_mapper(cls.model_construct())
    assert out == [{"type": "interrupted"}]
    assert state["interrupted"] is True


def test_on_token_emits_text_delta():
    out: list = []
    orig = cli_guest.emit
    cli_guest.emit = out.append
    try:
        chunk = SimpleNamespace(choices=[SimpleNamespace(delta=SimpleNamespace(content="ab"))])
        cli_guest.on_token(chunk)
        cli_guest.on_token(SimpleNamespace(choices=[SimpleNamespace(delta=SimpleNamespace(content=None))]))
        cli_guest.on_token(SimpleNamespace(choices=[SimpleNamespace(delta=SimpleNamespace(content=""))]))
        cli_guest.on_token(SimpleNamespace(choices=[]))
        cli_guest.on_token(SimpleNamespace(choices=None))
    finally:
        cli_guest.emit = orig
    assert out == [{"type": "text_delta", "text": "ab"}]


def test_recover_unmatched_actions_injects_error():
    action = ActionEvent.model_construct(
        action=object(), id="a1", tool_name="terminal", tool_call_id="c1"
    )
    injected: list = []
    conv = SimpleNamespace(
        state=SimpleNamespace(
            execution_status=ConversationExecutionStatus.RUNNING, events=[action]
        ),
        _on_event=injected.append,
    )
    cli_guest.recover_unmatched_actions(conv)
    assert conv.state.execution_status == ConversationExecutionStatus.ERROR
    assert len(injected) == 1
    err = injected[0]
    assert isinstance(err, AgentErrorEvent)
    assert err.tool_call_id == "c1"
    assert err.tool_name == "terminal"


def test_recover_noop_when_not_running():
    injected: list = []
    conv = SimpleNamespace(
        state=SimpleNamespace(execution_status=ConversationExecutionStatus.IDLE, events=[]),
        _on_event=injected.append,
    )
    cli_guest.recover_unmatched_actions(conv)
    assert injected == []


def test_recover_noop_when_already_observed():
    action = ActionEvent.model_construct(
        action=object(), id="a1", tool_name="terminal", tool_call_id="c1"
    )
    obs = ObservationEvent.model_construct(
        observation=SimpleNamespace(to_llm_content=[], is_error=False),
        tool_call_id="c1",
        action_id="a1",
    )
    injected: list = []
    conv = SimpleNamespace(
        state=SimpleNamespace(
            execution_status=ConversationExecutionStatus.RUNNING, events=[action, obs]
        ),
        _on_event=injected.append,
    )
    cli_guest.recover_unmatched_actions(conv)
    assert injected == []
