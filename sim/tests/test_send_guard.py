"""INV-01 in code: nothing in sim/ writes to a vehicle except the harness.

The rules, each paired with a check that it can fire (E-01):

1. No ``.send(`` anywhere in ``sim/`` (the done-when grep).
2. No pymavlink writer (``mav.<x>_send(``, ``set_mode``, ``arducopter_arm``,
   ``mavlink_connection``) outside ``sim/fly.py``.
3. Nothing but ``sim/fly.py`` itself and its own test names ``fly`` as a
   module, so no bridge can import the harness.
4. No Go code under ``cmd/sim-*`` or ``internal/`` mentions ``fly.py``:
   only the scenario runner's SITL driver starts it.
"""

from __future__ import annotations

import re
from pathlib import Path

SIM = Path(__file__).resolve().parent.parent
REPO = SIM.parent

SEND = re.compile(r"\.send\(")
WRITERS = re.compile(r"\.mav\.[a-z0-9_]+_send\(|\bset_mode\(|arducopter_(dis)?arm\(|mavlink_connection\(")
IMPORTS_FLY = re.compile(r"^\s*(import\s+fly\b|from\s+fly\s+import|from\s+sim\s+import\s+fly|import\s+sim\.fly)", re.M)
HARNESS = SIM / "fly.py"
HARNESS_TEST = SIM / "tests" / "test_fly.py"
THIS = Path(__file__).resolve()


def python_files() -> list[Path]:
    return sorted(p for p in SIM.rglob("*.py") if ".venv" not in p.parts and "out" not in p.parts)


def offenders(pattern: re.Pattern[str], files: list[Path]) -> list[str]:
    out = []
    for p in files:
        for n, line in enumerate(p.read_text(encoding="utf-8").splitlines(), 1):
            if pattern.search(line):
                out.append(f"{p.relative_to(REPO)}:{n}: {line.strip()}")
    return out


def test_no_send_call_anywhere_in_sim() -> None:
    files = [p for p in python_files() if p != THIS] + sorted(SIM.glob("*.sh"))
    assert offenders(SEND, files) == []


def test_no_pymavlink_writer_outside_the_harness() -> None:
    files = [p for p in python_files() if p not in (HARNESS, THIS, HARNESS_TEST)]
    assert offenders(WRITERS, files) == []


def test_the_harness_is_where_the_writers_are() -> None:
    # Presence: the patterns do match the harness, so an empty result
    # elsewhere means "none there", not "the pattern is broken".
    found = offenders(WRITERS, [HARNESS])
    assert any("command_long_send" in f for f in found)
    assert any("set_position_target_global_int_send" in f for f in found)
    assert any("mavlink_connection" in f for f in found)


def test_nothing_imports_the_harness() -> None:
    files = [p for p in python_files() if p not in (HARNESS_TEST, THIS)]
    assert offenders(IMPORTS_FLY, files) == []
    # Presence: the harness test does import it.
    assert offenders(IMPORTS_FLY, [HARNESS_TEST]) != []


def test_no_bridge_or_simulator_starts_the_harness() -> None:
    go = [*(REPO / "internal").rglob("*.go"), *(REPO / "cmd").rglob("*.go")]
    allowed = ("internal/runner/", "cmd/scenario/")
    hits = [f for f in offenders(re.compile(r"fly\.py"), go) if not any(a in f.replace("\\", "/") for a in allowed)]
    assert hits == []


def test_the_patterns_fire() -> None:
    assert SEND.search("sock.send(b'x')")
    assert not SEND.search("tx.sendto(b, addr)")
    assert WRITERS.search("master.mav.command_long_send(1, 1)")
    assert WRITERS.search("master.set_mode(4)")
    assert IMPORTS_FLY.search("from fly import Harness")
    assert IMPORTS_FLY.search("import fly")
    assert not IMPORTS_FLY.search("import flyer")
