"""Shared fixtures for partisan tests."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

import pytest

# Make cli_guest importable (it lives at the package root, not under tests/).
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))


@pytest.fixture(autouse=True)
def fake_api_key(monkeypatch):
    monkeypatch.setenv("LLM_API_KEY", "sk-fake")
    monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
    monkeypatch.delenv("LLM_BASE_URL", raising=False)
    monkeypatch.delenv("ANTHROPIC_BASE_URL", raising=False)


@pytest.fixture
def persist_dir(tmp_path, monkeypatch):
    d = tmp_path / "persist"
    monkeypatch.setenv("PARTISAN_PERSIST", str(d))
    return d


@pytest.fixture
def capture_emit(monkeypatch):
    """Collect every NDJSON object partisan emits (as dicts, not serialized)."""
    import cli_guest

    events: list[dict] = []
    monkeypatch.setattr(cli_guest, "emit", events.append)
    return events


@pytest.fixture
def make_args(tmp_path):
    def _make(**over):
        ns = argparse.Namespace(
            workspace=str(tmp_path),
            model=None,
            max_turns="20",
            task=None,
            serve=False,
            resume=None,
        )
        for k, v in over.items():
            setattr(ns, k, v)
        return ns

    return _make
