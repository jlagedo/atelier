"""Layer 2: run_once driven end-to-end by the deterministic fake model."""

from __future__ import annotations

import cli_guest
from tests.fake_llm import FakeScript, FakeToolCall, FakeTurn, patched_llm


def types(events):
    return [e.get("type") for e in events]


def first(events, t):
    return next(e for e in events if e.get("type") == t)


def test_streaming_text_turn(capture_emit, make_args):
    args = make_args(task="say hi")
    with patched_llm(FakeScript([FakeTurn(text="answer in several words")])):
        rc = cli_guest.run_once(args)
    assert rc == 0
    ts = types(capture_emit)
    assert ts[0] == "init"
    assert "text_delta" in ts  # streamed
    assert ts.index("text_delta") < ts.index("text") < ts.index("result")
    res = first(capture_emit, "result")
    assert res["subtype"] == "success"
    assert res["result"] == "answer in several words"


def test_tool_call_turn(capture_emit, make_args):
    args = make_args(task="run echo")
    script = FakeScript(
        [
            FakeTurn(tool=FakeToolCall(name="terminal", arguments={"command": "echo partisan-test"})),
            FakeTurn(text="done"),
        ]
    )
    with patched_llm(script):
        rc = cli_guest.run_once(args)
    assert rc == 0
    ts = types(capture_emit)
    assert "tool_use" in ts and "policy" in ts and "tool_result" in ts
    tu = first(capture_emit, "tool_use")
    assert tu["name"] == "terminal"
    assert first(capture_emit, "policy")["door"] == "compute"
    # tool_use precedes its tool_result
    assert ts.index("tool_use") < ts.index("tool_result")
    assert first(capture_emit, "result")["subtype"] == "success"


def test_error_turn(capture_emit, make_args):
    args = make_args(task="boom")
    with patched_llm(FakeScript([FakeTurn(error="simulated model failure")])):
        rc = cli_guest.run_once(args)
    assert rc == 1
    res = first(capture_emit, "result")
    assert res["subtype"] == "error_during_execution"
    assert any(e.get("type") == "error" for e in capture_emit)
