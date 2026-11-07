#!/usr/bin/env python3
"""Compare checked-in OpenAPI operations to actual staff and mobile route registrations."""
import json
from pathlib import Path
import re
import runpy

ROOT = Path(__file__).resolve().parents[1]
CONTRACTS = ROOT / "docs/contracts"
METHODS = {"get", "post", "put", "delete", "patch", "options", "head", "trace"}


def main():
    source = runpy.run_path(str(ROOT / "scripts/check-application-scope-inventory.py"))["source_inventory"]()
    server = (ROOT / "cmd/server/main.go").read_text()
    mounted = set(re.findall(r'^\s*(\w+)\.New\w*Module\(.*\.RegisterRoutes\(mux\)', server, re.M))
    if not mounted:
        raise ValueError("no production feature modules identified")
    routes = {name for name, file in source["routes"].items() if Path(file).parent.name in mounted}
    docs = {}
    for file in sorted(CONTRACTS.glob("*.openapi.json")):
        spec = json.loads(file.read_text())
        for path, item in spec["paths"].items():
            for method, operation in item.items():
                if method not in METHODS:
                    continue
                key = method.upper() + " " + path
                if key in docs:
                    raise ValueError(f"duplicate operation {key}: {docs[key][0]} and {file.name}")
                docs[key] = file.name, operation
    if routes != set(docs):
        raise ValueError(f"undocumented routes: {sorted(routes - set(docs))}; unmounted operations: {sorted(set(docs) - routes)}")

    permissions = dict(re.findall(r'(\w+)\s+Permission\s*=\s*"([^"]+)"',
                                  (ROOT / "internal/authz/authorization.go").read_text()))
    project_routes = (ROOT / "internal/features/applications/module.go").read_text()
    for key, constant in re.findall(r'register\("([^"]+)",\s*authz\.(\w+),', project_routes):
        file, operation = docs[key]
        if file == "staff-business.openapi.json":
            if operation.get("x-required-permission") != permissions[constant] or not operation.get("x-active-project-grant"):
                raise ValueError(f"project authorization contract diverged: {key}")
    print(f"{len(routes)} documented method/path pairs match the production feature modules and configured mobile routes.")


if __name__ == "__main__":
    main()
