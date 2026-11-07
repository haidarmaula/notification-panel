# Consumer contracts

| Consumer | Contract |
| --- | --- |
| Staff application registry and grants | [OpenAPI](application-registry.openapi.json) |
| Staff sessions, administration and scoped business resources | [OpenAPI](staff-business.openapi.json) |
| Installation lifecycle, mobile reports, staff send/schedule and campaign statistics | [OpenAPI](mobile-delivery.openapi.json) |
| Admission consumer | [Scoped send v1 JSON Schema](send-requested-v1.schema.json) |
| Delivery analytics or projection consumer | [Scoped update v2 JSON Schema](delivery-updated-v2.schema.json) |

HTTP examples contain synthetic identifiers, proofs and credentials. Send the
issued values for the selected application and deployment. Use a server-owned
gateway integration for the staff API key. Mobile clients use the project's
mobile bearer credential. Keep retry envelopes and installation secrets scoped
to the original account. Staff clients must replace both tokens after refresh.

Kafka messages use integer identifiers in JSON. Read int64 fields without
rounding them through a double. Validate the recorded schema version, exact
field names, per-campaign Kafka key and approved physical source. The reference
[consumer contract](../specs/m06-delivery-consumer.md) specifies duplicate,
version-gap, lineage and offset handling. Keep original bytes when computing
the consumer's conflict digest.

## Local checks

Run `python3 scripts/check-api-routes.py` for mounted route coverage and
permissions. Install `openapi-spec-validator==0.7.2` and
`jsonschema[format-nongpl]==4.23.0` in an isolated Python environment and run
`python scripts/check-api-contracts.py` to validate the documents and examples.
The Go tests decode the published Kafka examples with the real bounded parsers
and compare the closed property sets with the Go wire types. The opt-in HTTP
integration suite exercises live campaign acceptance, schedules and mobile
receipts against the examples. The actual frontend, mobile release builds
and external analytics readers have their own deployment acceptance gates.
