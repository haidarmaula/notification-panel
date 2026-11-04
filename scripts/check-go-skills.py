#!/usr/bin/env python3
"""Check the reviewed skill snapshot offline, without executing its content."""

import hashlib
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
SKILLS = ROOT / ".agents/skills"
LOCK = ROOT / "docs/tooling/go-skills.lock.json"


def tree_hash(directory):
    """Hash sorted relative paths and file hashes; reject symlinked content."""
    digest = hashlib.sha256()
    for path in sorted(directory.rglob("*")):
        if path.is_symlink():
            raise ValueError(f"skill snapshot contains a symlink: {path}")
        if path.is_file():
            digest.update(path.relative_to(directory).as_posix().encode() + b"\0")
            digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def main():
    lock = json.loads(LOCK.read_text())
    if (
        lock["version"] != 1
        or lock["repository"] != "https://github.com/samber/cc-skills-golang"
        or not re.fullmatch(r"[0-9a-f]{40}", lock["commit"])
        or lock["license"] != "MIT"
    ):
        raise ValueError("invalid upstream identity or unpinned skill revision")
    entries = lock["skills"]
    names = [entry["name"] for entry in entries]
    if len(names) != len(set(names)) or any(
        Path(name).name != name or not name.startswith("golang-") for name in names
    ):
        raise ValueError("invalid or duplicate locked skill names")
    actual = sorted(path.name for path in SKILLS.iterdir() if path.is_dir())
    if actual != sorted(names):
        raise ValueError("installed skill directories differ from the lock file")
    for entry in entries:
        directory = SKILLS / entry["name"]
        if directory.is_symlink() or not (directory / "SKILL.md").is_file():
            raise ValueError(f"invalid skill entry point: {entry['name']}")
        if tree_hash(directory) != entry["sha256"]:
            raise ValueError(f"skill snapshot drift: {entry['name']}")
    license_path = SKILLS / "cc-skills-golang.LICENSE"
    if license_path.is_symlink() or hashlib.sha256(license_path.read_bytes()).hexdigest() != lock["license_sha256"]:
        raise ValueError("upstream license differs from the lock file")
    print(f"Verified {len(entries)} pinned Go skills at {lock['commit']} (offline).")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f"Go skill check failed: {error}", file=sys.stderr)
        sys.exit(1)
