#!/usr/bin/env python3
"""Check onboarding navigation and optionally execute its infrastructure-free Go examples."""

import argparse
import html
import json
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
    "docs/local-development.md",
    "docs/setup.md",
    "docs/tooling/development-tools.md",
    "web/dashboard/README.md",
)
EXAMPLES = {
    "README.md": "First safe command",
    "docs/development-guide.md": "1. Start without infrastructure",
}
FENCE_OPEN = re.compile(r"^( {0,3})(`{3,}|~{3,})(.*)$")
FENCE_CLOSE = re.compile(r"^ {0,3}(`{3,}|~{3,})[ \t]*$")


def heading_ids(text):
    counts = {}
    ids = set()
    for heading in re.findall(r"^#{1,6}\s+(.+)$", text, re.M):
        slug = re.sub(r"[^\w\- ]", "", html.unescape(heading).lower()).replace(" ", "-")
        count = counts.get(slug, 0)
        counts[slug] = count + 1
        ids.add(f"{slug}-{count}" if count else slug)
    return ids


def fenced_blocks(text):
    blocks = []
    fence = None
    body = []
    for line in text.splitlines(keepends=True):
        content = line.rstrip("\r\n")
        if fence is None:
            opening = FENCE_OPEN.match(content)
            if opening is None:
                continue
            fence = (opening[1], opening[2][0], len(opening[2]), opening[3].strip())
            body = []
            continue

        closing = FENCE_CLOSE.match(content)
        if closing and closing[1][0] == fence[1] and len(closing[1]) >= fence[2]:
            indent, _, _, info = fence
            body_text = "".join(
                line[min(len(line) - len(line.lstrip(" ")), len(indent)) :]
                for line in body
            )
            language = info.split(maxsplit=1)[0] if info else ""
            blocks.append((language, body_text))
            fence = None
            body = []
            continue
        body.append(line)

    if fence is not None:
        raise ValueError("unclosed code fence")
    return blocks


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
    try:
        blocks = fenced_blocks(text)
    except ValueError as exc:
        raise ValueError(f"{relative}: {exc}") from exc
    for language, block in blocks:
        if language != "bash":
            continue
        result = subprocess.run(["bash", "-n"], input=block, text=True, capture_output=True, check=False)
        if result.returncode:
            raise ValueError(f"{relative}: invalid Bash syntax: {result.stderr.strip()}")


def check_pinned_tool_versions(root):
    setup = (root / "docs/setup.md").read_text()
    go_match = re.search(r"^go\s+(\S+)$", (root / "go.mod").read_text(), re.M)
    package = json.loads((root / "web/dashboard/package.json").read_text())
    pnpm_match = re.fullmatch(r"pnpm@([^\s]+)", package.get("packageManager", ""))
    if go_match is None or pnpm_match is None:
        raise ValueError("missing Go or pnpm source version pin")

    for tool, version in (("Go", go_match[1]), ("pnpm", pnpm_match[1])):
        row = re.search(rf"^\| {re.escape(tool)} \|([^\n]*)$", setup, re.M)
        if row is None or f"`{version}`" not in row[1]:
            raise ValueError(f"docs/setup.md: {tool} version must match its source pin {version}")


def example_commands(text, heading):
    marker = f"## {heading}\n"
    if marker not in text:
        raise ValueError(f"missing example section: {heading}")
    section = text.split(marker, 1)[1].split("\n## ", 1)[0]
    block = next((body for language, body in fenced_blocks(section) if language == "bash"), None)
    if block is None:
        raise ValueError(f"missing Bash example: {heading}")
    commands = [shlex.split(line) for line in block.splitlines() if line.strip()]
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
        check_pinned_tool_versions(ROOT)
        print(f"Passed local navigation, Bash syntax, and safe-example checks for {len(PAGES)} onboarding pages.")
        if args.run:
            run_examples(ROOT)
    except (OSError, ValueError, subprocess.TimeoutExpired) as exc:
        print(f"Onboarding check failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
