#!/usr/bin/env python3
"""Check onboarding navigation and optionally execute its infrastructure-free Go examples."""

import argparse
import html
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
PAGES = (
    "README.md",
    "docs/domain-model.md",
    "docs/notification-journey.md",
    "docs/architecture.md",
    "docs/development-guide.md",
)
EXAMPLES = {
    "README.md": "First safe command",
    "docs/development-guide.md": "1. Start without infrastructure",
}


def heading_ids(text):
    counts = {}
    ids = set()
    for heading in re.findall(r"^#{1,6}\s+(.+)$", text, re.M):
        slug = re.sub(r"[^\w\- ]", "", html.unescape(heading).lower()).replace(" ", "-")
        count = counts.get(slug, 0)
        counts[slug] = count + 1
        ids.add(f"{slug}-{count}" if count else slug)
    return ids


def check_page(root, relative):
    page = root / relative
    text = page.read_text()
    for target in re.findall(r"\]\(([^)]+)\)", text):
        if target.startswith(("https:", "http:", "mailto:")):
            continue
        path, _, anchor = target.partition("#")
        destination = (page.parent / path).resolve() if path else page
        if not destination.exists():
            raise ValueError(f"{relative}: missing link target {target}")
        if anchor and destination.suffix == ".md" and anchor not in heading_ids(destination.read_text()):
            raise ValueError(f"{relative}: missing heading {target}")
    if len(re.findall(r"^```", text, re.M)) % 2:
        raise ValueError(f"{relative}: unclosed code fence")
    for block in re.findall(r"^```bash\n(.*?)^```", text, re.M | re.S):
        result = subprocess.run(["bash", "-n"], input=block, text=True, capture_output=True, check=False)
        if result.returncode:
            raise ValueError(f"{relative}: invalid Bash syntax: {result.stderr.strip()}")


def example_commands(text, heading):
    marker = f"## {heading}\n"
    if marker not in text:
        raise ValueError(f"missing example section: {heading}")
    section = text.split(marker, 1)[1].split("\n## ", 1)[0]
    block = re.search(r"^```bash\n(.*?)^```", section, re.M | re.S)
    if not block:
        raise ValueError(f"missing Bash example: {heading}")
    commands = [shlex.split(line) for line in block[1].splitlines() if line.strip()]
    if not commands:
        raise ValueError(f"empty example: {heading}")
    for command in commands:
        validate_command(command)
    return commands


def validate_command(command):
    if len(command) < 4 or command[:2] not in (["go", "test"], ["go", "build"]):
        raise ValueError(f"not an infrastructure-free Go example: {shlex.join(command)}")
    if command[2] != "-mod=readonly":
        raise ValueError("onboarding examples must use -mod=readonly")
    if command[1] == "build":
        if command[3:] != ["./..."]:
            raise ValueError("only a package-wide Go build is allowed")
        return
    if command[3] not in ("./internal/jsonobject/...", "./internal/features/auth/..."):
        raise ValueError("only the credential-free onboarding test packages are allowed")
    remaining = command[4:]
    while remaining:
        option = remaining.pop(0)
        if option == "-count=1":
            continue
        if option == "-run" and remaining:
            remaining.pop(0)
            continue
        raise ValueError(f"unsupported onboarding test option: {option}")


def run_examples(root):
    commands = []
    for relative, heading in EXAMPLES.items():
        for command in example_commands((root / relative).read_text(), heading):
            if command not in commands:
                commands.append(command)
    go = shutil.which("go")
    if go is None:
        raise ValueError("Go is not on PATH; follow the developer guide's toolchain preflight")
    for command in commands:
        print(f"Running {shlex.join(command)}", flush=True)
        result = subprocess.run([go, *command[1:]], cwd=root, text=True, capture_output=True, timeout=600, check=False)
        output = result.stdout + result.stderr
        if output:
            print(output, end="" if output.endswith("\n") else "\n")
        if result.returncode:
            raise ValueError(f"example failed with exit {result.returncode}: {shlex.join(command)}")
        if command[1] == "test" and ("[no tests to run]" in output or "[no test files]" in output):
            raise ValueError(f"example did not exercise tests: {shlex.join(command)}")
    print(f"Passed {len(commands)} documented Go commands; no database, API listener, or provider was exercised.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", action="store_true", help="also run only the documented credential-free Go test/build examples")
    args = parser.parse_args()
    try:
        for page in PAGES:
            check_page(ROOT, page)
        for page, heading in EXAMPLES.items():
            example_commands((ROOT / page).read_text(), heading)
        print(f"Passed local navigation, Bash syntax, and safe-example checks for {len(PAGES)} onboarding pages.")
        if args.run:
            run_examples(ROOT)
    except (OSError, ValueError, subprocess.TimeoutExpired) as exc:
        print(f"Onboarding check failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
