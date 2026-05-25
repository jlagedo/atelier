"""Subprocess entrypoint that runs the real cli_guest with the deterministic fake model.

Used by the cross-language vitest (apps/desktop) so the SHIPPED TS client drives the real
partisan loop with no network / no API key. Invoke as:

    uv run python -m tests.fake_serve --serve --workspace <dir>

The scripted turns are read from the JSON file at $PARTISAN_FAKE_SCRIPT (a list of objects
with optional keys: text, tool{name,arguments,id}, error, stall). cli_guest.py is unchanged;
this shim only installs the litellm monkeypatch before calling cli_guest.main().
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path


def _load_script():
    from tests.fake_llm import FakeScript, FakeToolCall, FakeTurn

    path = os.environ.get("PARTISAN_FAKE_SCRIPT")
    if not path:
        return FakeScript([FakeTurn(text="hello from the fake model")])
    data = json.loads(Path(path).read_text())
    turns = []
    for t in data:
        tool = t.get("tool")
        turns.append(
            FakeTurn(
                text=t.get("text"),
                tool=FakeToolCall(**tool) if tool else None,
                error=t.get("error"),
                stall=float(t.get("stall", 0.0)),
            )
        )
    return FakeScript(turns)


def main() -> int:
    os.environ.setdefault("LLM_API_KEY", "sk-fake")
    os.environ.setdefault(
        "PARTISAN_PERSIST",
        str(Path(os.environ.get("TMPDIR", "/tmp")) / "partisan-fake-persist"),
    )

    # Import cli_guest first (sets up the NDJSON stdout redirect and loads openhands),
    # then install the fake over the SDK's litellm entry points.
    import cli_guest

    from tests.fake_llm import patched_llm

    script = _load_script()
    with patched_llm(script):
        return cli_guest.main()


if __name__ == "__main__":
    sys.exit(main())
