"""stop_sitl.sh against real process groups: the success path and the failure path.

The predecessor's stop script exited 1 in silence whenever the teardown
worked (pgrep's "no match" under pipefail). These tests start stand-in
process groups the way run_sitl.sh does (setsid), record them in a pid
file, and run the script: a teardown that worked must say what it stopped
and exit 0; a survivor must be named and exit 1.
"""

from __future__ import annotations

import os
import shutil
import signal
import subprocess
import sys
import time
import uuid
from pathlib import Path

import pytest

SIM = Path(__file__).resolve().parent.parent
SCRIPT = SIM / "stop_sitl.sh"

pytestmark = pytest.mark.skipif(
    sys.platform != "linux" or shutil.which("setsid") is None or shutil.which("pgrep") is None,
    reason="needs Linux with setsid and pgrep (CI and WSL)",
)


def start_group(tag: str) -> int:
    """A process group whose leader and child match the tag, like sim_vehicle + arducopter.

    Started from a shell that exits at once, as run_sitl.sh does, so the
    group is reparented and reaped by init rather than left a zombie of
    this test process (a zombie still answers kill -0).
    """
    r = subprocess.run(
        ["bash", "-c", f"setsid bash -c 'sleep 300 & exec -a {tag}-leader sleep 300' >/dev/null 2>&1 & echo $!"],
        capture_output=True,
        text=True,
        check=True,
    )
    pid = int(r.stdout.strip())
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        if subprocess.run(["pgrep", "-g", str(pid)], capture_output=True).returncode == 0:
            return pid
        time.sleep(0.05)
    raise AssertionError("the stand-in group did not start")


def run_stop(out_dir: Path, pattern: str) -> subprocess.CompletedProcess[str]:
    env = dict(os.environ, SITL_OUT_DIR=str(out_dir), SITL_PROC_PATTERN=pattern, SITL_TERM_WAIT_S="5")
    return subprocess.run(["bash", str(SCRIPT)], capture_output=True, text=True, env=env, timeout=60)


def alive(pgid: int) -> bool:
    try:
        os.killpg(pgid, 0)  # type: ignore[attr-defined,unused-ignore]
    except ProcessLookupError:
        return False
    return True


def test_a_teardown_that_worked_says_so_and_exits_0(tmp_path: Path) -> None:
    tag = f"fake-sitl-{uuid.uuid4().hex[:8]}"
    groups = [start_group(tag), start_group(tag)]
    (tmp_path / "sitl.pids").write_text("".join(f"{g}\n" for g in groups))
    r = run_stop(tmp_path, tag)
    assert r.returncode == 0, r.stdout + r.stderr
    assert "stopped 2 process group(s)" in r.stdout
    assert f"{tag}-leader" in r.stdout, "it says what it stopped"
    assert "nothing left running" in r.stdout
    assert not any(alive(g) for g in groups)
    assert (tmp_path / "sitl.pids").read_text() == ""


def test_nothing_to_stop_exits_0(tmp_path: Path) -> None:
    r = run_stop(tmp_path, f"never-{uuid.uuid4().hex}")
    assert r.returncode == 0, r.stdout + r.stderr
    assert "nothing to stop" in r.stdout


def test_already_exited_groups_exit_0(tmp_path: Path) -> None:
    tag = f"fake-sitl-{uuid.uuid4().hex[:8]}"
    g = start_group(tag)
    os.killpg(g, signal.SIGKILL)  # type: ignore[attr-defined,unused-ignore]
    deadline = time.monotonic() + 5
    while alive(g) and time.monotonic() < deadline:
        time.sleep(0.05)
    (tmp_path / "sitl.pids").write_text(f"{g}\n")
    r = run_stop(tmp_path, tag)
    assert r.returncode == 0, r.stdout + r.stderr
    assert "already exited" in r.stdout


def test_a_survivor_is_named_and_exits_1(tmp_path: Path) -> None:
    # A matching process that is not in the pid file survives the stop.
    tag = f"fake-sitl-{uuid.uuid4().hex[:8]}"
    recorded = start_group(tag)
    stray = start_group(tag)
    (tmp_path / "sitl.pids").write_text(f"{recorded}\n")
    try:
        r = run_stop(tmp_path, tag)
        assert r.returncode == 1, r.stdout + r.stderr
        assert "WARNING" in r.stderr and f"{tag}-leader" in r.stderr
        assert not alive(recorded)
    finally:
        os.killpg(stray, signal.SIGKILL)  # type: ignore[attr-defined,unused-ignore]


def test_a_stray_with_no_pid_file_exits_1(tmp_path: Path) -> None:
    tag = f"fake-sitl-{uuid.uuid4().hex[:8]}"
    stray = start_group(tag)
    try:
        r = run_stop(tmp_path, tag)
        assert r.returncode == 1, r.stdout + r.stderr
        assert "simulator process(es) are running" in r.stderr
    finally:
        os.killpg(stray, signal.SIGKILL)  # type: ignore[attr-defined,unused-ignore]


def test_the_default_pattern_is_scoped_to_this_fleet(tmp_path: Path) -> None:
    """Without SITL_PROC_PATTERN the check matches processes naming this fleet's
    instance directories, and leaves another checkout's fleet alone."""
    ours = f"{tmp_path}/instance-0/sysid.parm"
    theirs = f"/elsewhere-{uuid.uuid4().hex[:8]}/sim/out/instance-0/sysid.parm"
    stray_ours = start_group_with_arg(ours)
    stray_theirs = start_group_with_arg(theirs)
    try:
        env = dict(os.environ, SITL_OUT_DIR=str(tmp_path), SITL_TERM_WAIT_S="5")
        env.pop("SITL_PROC_PATTERN", None)
        r = subprocess.run(["bash", str(SCRIPT)], capture_output=True, text=True, env=env, timeout=60)
        assert r.returncode == 1, r.stdout + r.stderr  # ours is a leftover
        assert ours in r.stderr and theirs not in r.stderr
        os.killpg(stray_ours, signal.SIGKILL)  # type: ignore[attr-defined,unused-ignore]
        deadline = time.monotonic() + 5
        while alive(stray_ours) and time.monotonic() < deadline:
            time.sleep(0.05)
        r = subprocess.run(["bash", str(SCRIPT)], capture_output=True, text=True, env=env, timeout=60)
        assert r.returncode == 0, r.stdout + r.stderr  # theirs is not ours to report
        assert alive(stray_theirs), "another fleet is never touched"
    finally:
        for g in (stray_ours, stray_theirs):
            if alive(g):
                os.killpg(g, signal.SIGKILL)  # type: ignore[attr-defined,unused-ignore]


def start_group_with_arg(arg: str) -> int:
    r = subprocess.run(
        ["bash", "-c", f"setsid bash -c 'sleep 300; :' stand-in {arg} >/dev/null 2>&1 & echo $!"],
        capture_output=True,
        text=True,
        check=True,
    )
    pid = int(r.stdout.strip())
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        if subprocess.run(["pgrep", "-g", str(pid)], capture_output=True).returncode == 0:
            return pid
        time.sleep(0.05)
    raise AssertionError("the stand-in group did not start")
