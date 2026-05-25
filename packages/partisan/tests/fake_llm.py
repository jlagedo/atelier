"""Deterministic fake model for partisan tests — no network, no API key.

partisan always runs the LLM with ``stream=True``, so the OpenHands SDK asserts the
completion call returns a litellm ``CustomStreamWrapper`` (llm.py:1564 sync / 1623 async),
drains it through ``on_token``, then reassembles via ``litellm.stream_chunk_builder``. We
therefore can't just return a plain ``ModelResponse``; we build a real ``CustomStreamWrapper``
around a scripted response (text or tool call). For the async path we stall *after* each
chunk so an ``interrupt()`` can land mid-stream deterministically.

This patches the SDK's module-global litellm entry points; ``cli_guest.py`` is untouched.
"""

from __future__ import annotations

import asyncio
import json
import time
import uuid
from contextlib import contextmanager
from dataclasses import dataclass, field

from litellm import CustomStreamWrapper, ModelResponse
from litellm.litellm_core_utils.litellm_logging import Logging as LiteLLMLogging
from litellm.llms.base_llm.base_model_iterator import MockResponseIterator
from litellm.types.utils import (
    ChatCompletionMessageToolCall,
    Choices,
    Function,
)
from litellm.types.utils import Message as LiteLLMMessage

import openhands.sdk.llm.llm as _llmmod


@dataclass
class FakeToolCall:
    name: str
    arguments: dict = field(default_factory=dict)
    id: str = "call_fake_1"


@dataclass
class FakeTurn:
    """One scripted LLM completion. A tool-using agent turn is two FakeTurns:
    a ``tool`` response, then a ``text`` response after the tool result."""

    text: str | None = None
    tool: FakeToolCall | None = None
    error: str | None = None
    stall: float = 0.0  # async sleep after each chunk -> interrupt window


class FakeScript:
    def __init__(self, turns: list[FakeTurn]):
        self._turns = list(turns)
        self._i = 0

    def next(self) -> FakeTurn:
        if self._i < len(self._turns):
            t = self._turns[self._i]
            self._i += 1
            return t
        # Over-called (e.g. agent didn't finish): return a finishing text turn.
        return FakeTurn(text="(fake: no more scripted turns)")


def _model_response(turn: FakeTurn, model: str) -> ModelResponse:
    if turn.tool is not None:
        msg = LiteLLMMessage(
            role="assistant",
            content=None,
            tool_calls=[
                ChatCompletionMessageToolCall(
                    id=turn.tool.id,
                    type="function",
                    function=Function(
                        name=turn.tool.name, arguments=json.dumps(turn.tool.arguments)
                    ),
                )
            ],
        )
    else:
        msg = LiteLLMMessage(role="assistant", content=turn.text or "")
    return ModelResponse(choices=[Choices(index=0, message=msg)], model=model)


def _logging(model: str, messages) -> LiteLLMLogging:
    lo = LiteLLMLogging(
        model=model,
        messages=messages or [],
        stream=True,
        call_type="completion",
        start_time=time.time(),
        litellm_call_id=str(uuid.uuid4()),
        function_id="partisan-fake",
    )
    lo.update_environment_variables(
        litellm_params={}, optional_params={}, model=model, user=None
    )
    return lo


def _wrap(completion_stream, model: str, messages) -> CustomStreamWrapper:
    return CustomStreamWrapper(
        completion_stream=completion_stream,
        model=model,
        custom_llm_provider="anthropic",
        logging_obj=_logging(model, messages),
    )


def make_fake_completion(script: FakeScript):
    """Sync replacement for openhands.sdk.llm.llm.litellm_completion (run_once)."""

    def fake(*_args, **kwargs):
        turn = script.next()
        if turn.error:
            raise RuntimeError(turn.error)
        model = kwargs.get("model", "anthropic/fake")
        messages = kwargs.get("messages")
        chunks = list(MockResponseIterator(model_response=_model_response(turn, model)))

        def gen():
            yield from chunks

        return _wrap(gen(), model, messages)

    return fake


def make_fake_acompletion(script: FakeScript):
    """Async replacement for openhands.sdk.llm.llm.litellm_acompletion (run_serve/arun)."""

    async def fake(*_args, **kwargs):
        turn = script.next()
        if turn.error:
            raise RuntimeError(turn.error)
        model = kwargs.get("model", "anthropic/fake")
        messages = kwargs.get("messages")
        chunks = list(MockResponseIterator(model_response=_model_response(turn, model)))

        async def agen():
            for ch in chunks:
                yield ch
                if turn.stall:
                    await asyncio.sleep(turn.stall)  # interrupt can land here

        return _wrap(agen(), model, messages)

    return fake


@contextmanager
def patched_llm(script: FakeScript):
    real_c = _llmmod.litellm_completion
    real_a = _llmmod.litellm_acompletion
    _llmmod.litellm_completion = make_fake_completion(script)
    _llmmod.litellm_acompletion = make_fake_acompletion(script)
    try:
        yield
    finally:
        _llmmod.litellm_completion = real_c
        _llmmod.litellm_acompletion = real_a
