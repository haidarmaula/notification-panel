"""Validate checked-in OpenAPI and event documents and their wire examples.

Run with openapi-spec-validator 0.7.2 and jsonschema[format-nongpl] 4.23.0.
"""
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from openapi_spec_validator import validate

CONTRACTS = Path(__file__).resolve().parents[1] / "docs" / "contracts"


def load(path):
    with path.open(encoding="utf-8") as source:
        return json.load(source)


def examples(document, node):
    """Validate examples attached to schema-bearing request/response media."""
    count = 0
    if isinstance(node, dict):
        schema = node.get("schema")
        if isinstance(schema, dict):
            checker = Draft202012Validator(document, format_checker=FormatChecker()).evolve(schema=schema)
            if "example" in node:
                checker.validate(node["example"])
                count += 1
            for item in node.get("examples", {}).values():
                if "value" in item:
                    checker.validate(item["value"])
                    count += 1
        for child in node.values():
            count += examples(document, child)
    elif isinstance(node, list):
        for child in node:
            count += examples(document, child)
    return count


def main():
    for path in sorted(CONTRACTS.glob("*.openapi.json")):
        document = load(path)
        validate(document)
        count = examples(document, document)
        print(f"{path.name}: {len(document['paths'])} paths, {count} media examples")
    for path in sorted(CONTRACTS.glob("*.schema.json")):
        schema = load(path)
        Draft202012Validator.check_schema(schema)
        checker = Draft202012Validator(schema, format_checker=FormatChecker())
        for example in schema["examples"]:
            checker.validate(example)
        print(f"{path.name}: validated {len(schema['examples'])} event examples")


if __name__ == "__main__":
    main()
