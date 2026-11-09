#!/usr/bin/env python3
"""Run Go tests with retrievable JSON logs; never truncate failure diagnostics."""

import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


def summarize(raw, returncode):
    """Suppress successful-test chatter only for complete, recognized Go events."""
    events = []
    terminal = {}
    tests = {"pass": 0, "skip": 0, "fail": 0}
    known = {"start", "run", "pause", "cont", "output", "pass", "skip", "fail", "bench"}
    try:
        for line in raw.splitlines():
            event = json.loads(line)
            if not isinstance(event, dict) or event.get("Action") not in known:
                raise ValueError("unrecognized event")
            if not isinstance(event.get("Package"), str):
                raise ValueError("missing package")
            if "Output" in event and not isinstance(event["Output"], str):
                raise ValueError("invalid output")
            events.append(event)
            action = event["Action"]
            if action in tests:
                if event.get("Test"):
                    tests[action] += 1
                else:
                    terminal[event["Package"]] = action
        packages = {event["Package"] for event in events}
        if not packages or packages != set(terminal):
            raise ValueError("incomplete package results")
        if (returncode == 0) != ("fail" not in terminal.values() and tests["fail"] == 0):
            raise ValueError("exit status disagrees with events")
    except (ValueError, TypeError):
        return "Unrecognized/incomplete test output; showing full log:\n" + raw

    lines = [f"Go tests: {tests['pass']} passed, {tests['skip']} skipped, "
             f"{tests['fail']} failed; {len(terminal)} packages.\n"]
    for package, status in terminal.items():
        if status != "pass":
            lines.append(f"{status.upper()} {package}\n")
    # Keep all output from failed packages, and skip reasons from skipped tests.
    skipped = {(e["Package"], e.get("Test")) for e in events if e["Action"] == "skip"}
    for event in events:
        if event["Action"] == "output" and (
            terminal[event["Package"]] == "fail"
            or (event["Package"], event.get("Test")) in skipped
        ):
            lines.append(event.get("Output", ""))
    return "".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default="go", help="Go executable (default: PATH go)")
    parser.add_argument("test_args", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    test_args = args.test_args
    if test_args[:1] == ["--"]:
        test_args = test_args[1:]
    root = Path(__file__).resolve().parent.parent / "tmp" / "codex-logs"
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    directory = Path(tempfile.mkdtemp(prefix="go-test-", dir=root))
    log = directory / "full.log"
    start = time.monotonic()
    with log.open("wb") as output:
        try:
            child = subprocess.Popen([args.go, "test", "-json", *test_args],
                                     stdout=output, stderr=subprocess.STDOUT)
            try:
                code = child.wait()
            except KeyboardInterrupt:
                child.send_signal(signal.SIGINT)
                code = child.wait()
        except OSError as error:
            output.write((str(error) + "\n").encode())
            code = 127
    raw = log.read_text(errors="replace")
    try:
        summary = summarize(raw, code)
    except Exception as error:
        # Reporting bugs must never turn a failed command into a pass or hide output.
        summary = f"Summary failed ({type(error).__name__}); full output:\n{raw}"
    status = code if code >= 0 else 128 - code
    rendered = summary + f"Exit: {status}. Full log: {log}\n"
    (directory / "summary.txt").write_text(rendered)
    (directory / "metrics.json").write_text(json.dumps({
        "command": [args.go, "test", "-json", *test_args],
        "returncode": code, "shell_exit_code": status,
        "seconds": round(time.monotonic() - start, 4),
        "raw_bytes": log.stat().st_size,
        "summary_bytes": len(rendered.encode()),
        "measurement": "UTF-8 bytes, not model tokens",
    }, indent=2) + "\n")
    print(rendered, end="")
    return status


if __name__ == "__main__":
    sys.exit(main())
