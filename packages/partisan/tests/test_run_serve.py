"""Layer 2: run_serve (async) driven over a stdin pipe with the fake model.

Covers the serve loop: streaming turn, tool turn, mid-stream interrupt, export_context,
busy guard, and a real persistence resume round-trip.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import sys

import pytest

import cli_guest
from tests.fake_llm import FakeScript, FakeToolCall, FakeTurn, patched_llm


def types(events):
    return [e.get("type") for e in events]


def first(events, t):
    return next(e for e in events if e.get("type") == t)


async def wait_for(capture, pred, timeout=10.0):
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    while loop.time() < deadline:
        if any(pred(e) for e in capture):
            return
        await asyncio.sleep(0.01)
    raise TimeoutError(f"event not seen in {timeout}s; got {types(capture)}")


class Serve:
    """Runs cli_guest.run_serve over an os.pipe stdin; lets the test send controls."""

    def __init__(self, args, script, capture):
        self.args = args
        self.script = script
        self.capture = capture

    async def __aenter__(self):
        self._r, self._w = os.pipe()
        self._old_stdin = sys.stdin
        sys.stdin = os.fdopen(self._r, "r")
        self._patch = patched_llm(self.script)
        self._patch.__enter__()
        self.task = asyncio.create_task(cli_guest.run_serve(self.args))
        return self

    def send(self, obj):
        os.write(self._w, (json.dumps(obj) + "\n").encode())

    async def wait(self, pred, timeout=10.0):
        await wait_for(self.capture, pred, timeout)

    async def finish(self, timeout=15.0):
        return await asyncio.wait_for(self.task, timeout=timeout)

    async def __aexit__(self, *exc):
        try:
            if not self.task.done():
                self.task.cancel()
                with contextlib.suppress(Exception):
                    await self.task
        finally:
            try:
                os.close(self._w)
            except OSError:
                pass
            sys.stdin = self._old_stdin
            self._patch.__exit__(None, None, None)


def is_type(t):
    return lambda e: e.get("type") == t


@pytest.mark.asyncio
async def test_single_text_turn(capture_emit, make_args, persist_dir):
    args = make_args(serve=True)
    async with Serve(args, FakeScript([FakeTurn(text="hi there friend")]), capture_emit) as s:
        s.send({"type": "user", "text": "hello"})
        await s.wait(is_type("turn_done"))
        s.send({"type": "close"})
        rc = await s.finish()
    assert rc == 0
    ts = types(capture_emit)
    assert ts[0] == "init"
    assert "text_delta" in ts
    assert "text" in ts
    assert first(capture_emit, "result")["subtype"] == "success"
    assert "turn_done" in ts


@pytest.mark.asyncio
async def test_tool_turn(capture_emit, make_args, persist_dir):
    args = make_args(serve=True)
    script = FakeScript(
        [
            FakeTurn(tool=FakeToolCall(name="terminal", arguments={"command": "echo hi"})),
            FakeTurn(text="all done"),
        ]
    )
    async with Serve(args, script, capture_emit) as s:
        s.send({"type": "user", "text": "run it"})
        await s.wait(is_type("turn_done"))
        s.send({"type": "close"})
        await s.finish()
    ts = types(capture_emit)
    assert "tool_use" in ts and "policy" in ts and "tool_result" in ts
    assert first(capture_emit, "policy")["door"] == "compute"
    assert first(capture_emit, "result")["subtype"] == "success"


@pytest.mark.asyncio
async def test_interrupt_mid_stream(capture_emit, make_args, persist_dir):
    args = make_args(serve=True)
    # stall after the first chunk -> interrupt lands mid-stream
    script = FakeScript([FakeTurn(text="a long streamed answer", stall=3.0)])
    async with Serve(args, script, capture_emit) as s:
        s.send({"type": "user", "text": "go"})
        await s.wait(is_type("text_delta"))  # gate on observed delta, never a fixed sleep
        s.send({"type": "interrupt"})
        await s.wait(is_type("turn_done"))
        s.send({"type": "close"})
        await s.finish()
    ts = types(capture_emit)
    assert "text_delta" in ts
    assert "interrupted" in ts
    assert first(capture_emit, "result")["subtype"] == "interrupted"


@pytest.mark.asyncio
async def test_export_context(capture_emit, make_args, persist_dir):
    args = make_args(serve=True)
    async with Serve(args, FakeScript([FakeTurn(text="exported answer")]), capture_emit) as s:
        s.send({"type": "user", "text": "hi"})
        await s.wait(is_type("turn_done"))
        s.send({"type": "export_context"})
        await s.wait(is_type("context"))
        rc = await s.finish()
    assert rc == 0
    ctx = first(capture_emit, "context")
    assert "sessionId" in ctx
    assert isinstance(ctx["transcript"], list)
    assert any(e.get("type") == "result" for e in ctx["transcript"])


@pytest.mark.asyncio
async def test_busy_guard(capture_emit, make_args, persist_dir):
    args = make_args(serve=True)
    script = FakeScript([FakeTurn(text="slow answer", stall=2.0)])
    async with Serve(args, script, capture_emit) as s:
        s.send({"type": "user", "text": "first"})
        await s.wait(is_type("text_delta"))
        s.send({"type": "user", "text": "second"})  # while first still running
        await s.wait(lambda e: e.get("type") == "error" and "busy" in e.get("message", ""))
        await s.wait(is_type("turn_done"))
        s.send({"type": "close"})
        await s.finish()
    assert any(e.get("type") == "error" and "busy" in e.get("message", "") for e in capture_emit)


@pytest.mark.asyncio
async def test_resume_roundtrip(capture_emit, make_args, persist_dir):
    # Session 1: one turn, capture the sessionId, close.
    args1 = make_args(serve=True)
    async with Serve(args1, FakeScript([FakeTurn(text="first session")]), capture_emit) as s:
        s.send({"type": "user", "text": "hi"})
        await s.wait(is_type("turn_done"))
        s.send({"type": "close"})
        await s.finish()
    session_id = first(capture_emit, "init")["sessionId"]

    # Session 2: resume the same id; a fresh turn should run.
    capture2: list = []
    import cli_guest as cg

    orig = cg.emit
    cg.emit = capture2.append
    try:
        args2 = make_args(serve=True, resume=session_id)
        async with Serve(args2, FakeScript([FakeTurn(text="second session")]), capture2) as s:
            s.send({"type": "user", "text": "again"})
            await s.wait(is_type("turn_done"))
            s.send({"type": "close"})
            await s.finish()
    finally:
        cg.emit = orig
    assert first(capture2, "init")["sessionId"] == session_id
    assert first(capture2, "result")["subtype"] == "success"
