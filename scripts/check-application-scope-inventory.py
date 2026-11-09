#!/usr/bin/env python3
"""Check M03b's reviewed source inventory, not runtime isolation or authorization.

Only the repository's current literal/const-prefix route registration forms are
supported. A registration that cannot be resolved fails instead of being skipped.
Run from any directory; no database, credentials, dependencies or network needed.
"""
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "docs/contracts/application-scope-inventory.json"


def source_inventory():
    tables = set()
    for path in (ROOT / "db/migrations").glob("*.up.sql"):
        tables.update(re.findall(r"\bCREATE TABLE\s+(\w+)\s*\(", path.read_text(), re.I))
    queries = {}
    for path in sorted((ROOT / "db/queries").glob("*.sql")):
        names = re.findall(r"^-- name: (\w+) :\w+", path.read_text(), re.M)
        if not names or len(names) != len(set(names)):
            raise ValueError(f"missing/duplicate query names: {path.name}")
        queries[str(path.relative_to(ROOT))] = sorted(names)
    routes = {}
    for path in sorted((ROOT / "internal/features").glob("*/*.go")):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text()
        prefix_match = re.search(r'const prefix = "([^"\n]+)"', text)
        prefix = prefix_match[1] if prefix_match else None
        for line in text.splitlines():
            line = line.strip()
            if not line.startswith(("mux.HandleFunc(", "mux.Handle(")):
                continue
            # applications.Module uses a local register wrapper with a literal
            # pattern; its single mux.HandleFunc(pattern, ...) is handled below.
            expr = line.split("(", 1)[1].split(",", 1)[0].strip()
            if expr == "pattern" and path.parent.name == "applications":
                continue
            pattern = ""
            for part in expr.split("+"):
                part = part.strip()
                if part == "prefix" and prefix is not None:
                    pattern += prefix
                elif re.fullmatch(r'"[^"\n]*"', part):
                    pattern += json.loads(part)
                else:
                    raise ValueError(f"unresolved route expression in {path}: {expr}")
            if pattern in routes:
                raise ValueError(f"duplicate route: {pattern}")
            routes[pattern] = str(path.relative_to(ROOT))
        if path.parent.name == "applications" and path.name == "module.go":
            for expr in re.findall(r'^\s*register\(([^,\n]+),', text, re.M):
                pattern = ""
                for part in expr.split("+"):
                    part = part.strip()
                    if part == "prefix" and prefix is not None:
                        pattern += prefix
                    elif re.fullmatch(r'"[^"\n]*"', part):
                        pattern += json.loads(part)
                    else:
                        raise ValueError(f"unresolved application route expression in {path}: {expr}")
                if pattern in routes:
                    raise ValueError(f"duplicate route: {pattern}")
                routes[pattern] = str(path.relative_to(ROOT))
    commands = sorted(str(p.relative_to(ROOT)) for p in (ROOT / "cmd").glob("*/main.go"))
    return {"tables": sorted(tables), "queries": queries, "routes": routes, "commands": commands}


def main():
    manifest = json.loads(MANIFEST.read_text())
    actual = source_inventory()
    expected = {
        "tables": sorted(t["name"] for t in manifest["tables"]),
        "queries": {q["path"]: sorted(q["names"]) for q in manifest["query_files"]},
        "routes": {r["pattern"]: r["source"] for r in manifest["routes"]},
        "commands": sorted(c["path"] for c in manifest["commands"]),
    }
    if len(expected["tables"]) != len(set(expected["tables"])):
        raise ValueError("duplicate table entries")
    for group, key in (("query_files", "queries"), ("routes", "routes"), ("commands", "commands")):
        if len(manifest[group]) != len(expected[key]):
            raise ValueError(f"duplicate entries in {group}")
    for query in manifest["query_files"]:
        if len(query["names"]) != len(set(query["names"])):
            raise ValueError(f"duplicate query entries: {query['path']}")
    failed = False
    for kind in actual:
        if actual[kind] != expected[kind]:
            print(f"Inventory drift in {kind}; review source and update the manifest.", file=sys.stderr)
            failed = True
    valid = {"application-legacy", "global-control", "registry-control", "mixed-audit", "application-scoped"}
    for group in ("tables", "query_files", "routes"):
        for item in manifest[group]:
            if item["scope"] not in valid:
                raise ValueError(f"unknown scope in {group}: {item['scope']}")
    if failed:
        return 1
    print(f"Inventory matches source: {len(actual['tables'])} tables, "
          f"{sum(len(v) for v in actual['queries'].values())} SQL queries, "
          f"{len(actual['routes'])} HTTP operations, {len(actual['commands'])} binaries.")
    print("This checks inventory drift only; it does not establish application isolation.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, KeyError, OSError) as error:
        print(f"Inventory check failed: {error}", file=sys.stderr)
        sys.exit(1)
