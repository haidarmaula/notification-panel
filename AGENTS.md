**Go quality and documentation policy:** Read `docs/go-standards.md` before Go
changes and apply the relevant pinned skills under `.agents/skills/` on demand.
The project profile explicitly supersedes conflicting community defaults.
Maintained documentation and explanatory code comments must be in English.
Skill provenance and update rules are in `docs/tooling/go-skills.md`.

# Project instructions

Go notification backend; module/import prefix `hello`, not `notification-panel`.
Use code and symbol Godoc as the map; implementation wins over stale comments.
README paths/tree are stale; config is split into `internal/config/*.config.go`.
Before fixing a bug, search `code-review.md`; update the finding when fixed.

## Navigation

| Task | Start here |
|---|---|
| HTTP behavior | `internal/features/<feature>/{routes,handler,service,dto,model}.go` |
| Composition | `cmd/{server,worker,scheduler,bootstrap}/main.go`; feature `module.go` |
| Persistence | feature interfaces → `internal/database/repository/` → generated `internal/database/sqlc/` |
| SQL/schema | `db/queries/`, `db/migrations/`, `db/sqlc.yaml` |
| Permissions/errors | `internal/authz/`, `internal/middleware/`, `pkg/response/` |
| API/schema docs | `docs/api/<module>-api.md`, `docs/database.md` |

Server composes seven features and publishes send requests; worker consumes Kafka,
sends via OneSignal, and publishes delivery updates; scheduler queues due work;
bootstrap seeds roles/first super-admin. Each process shares one pool/query set.
Config is loaded once per binary and must remain immutable.

## Required context, on demand

`docs/agent-reference.md` preserves the detailed project instructions. Search its
headings and read the relevant sections before the corresponding work:

- Tests, mocks, integrations: **Commands** (fixtures, coverage limits, opt-in setup).
- SQL, repository interfaces, CI: **Generated code and migrations** and **Commands**.
- Local startup, containers, config: **Local environment gotchas** and **Architecture**.
- Notifications, providers, worker, Kafka, scheduling, status, authz, audit,
  DB URLs, or list/filter changes: **Known landmines** and **Architecture**.

Read touched symbols' comments and bodies; provider/Kafka interface comments
contain known stale claims.

## Context budget

Tool results, not prose, dominate token spend. These apply to every task:

- Never `Read` a whole file to get oriented. Grep the symbol, then read with
  offset/limit around the hit. Whole-file reads are for files the task rewrites.
- `.rgignore` keeps generated sqlc/mocks, skill eval fixtures, API contracts and
  diagrams out of exploratory search. Name the path explicitly to search them.
- `code-review.md` is long: grep the finding or keyword, never read it whole.
- Load at most one `.agents/skills/` body per task, and only the section that
  applies. Never read the collection or its `references/` speculatively.
- Delegate multi-file exploration to a search subagent and ask for a conclusion
  under 200 words, so file dumps stay out of the main context.
- Run the narrowest test that proves the change before any broader sweep.
- Do not re-read a file you just edited, and do not restate diffs or narrate the
  search; report findings, decisions and results.

## Checks and invariants

- Start with `go test ./internal/features/<feature>/...` or a relevant `-run` test;
  broaden to `go test ./...` and `go build ./...` for shared changes/required checks.
  Use `GOCACHE=/tmp/hello-go-review-cache` if the default cache is unwritable.
- Keep tests quiet. For verbose/JSON diagnostics use
  `python3 scripts/quiet-go-test.py -- <go test arguments>`; it retains full logs
  and exit status. Limits/retrieval: `docs/analysis/codex-log-pilot.md`.
- CI exists: `bash scripts/ci.sh <format|modules|build|test|vet|vuln>`; these checks
  need no `.env`. Go version is in `go.mod`; do not suppress network/scanner failures.
  Unit checks do not establish integration or production readiness.
- Interface changes consumed by mocks require `go tool mockery`; no global binary.
- Never hand-edit `internal/database/sqlc/`. Change SQL, create migrations before
  `make sqlc`, and retain generated changes. Migrations need paired six-digit
  `.up.sql`/`.down.sql` files. Every Make target requires `.env`; keep its include.
- Keep current DB-backed authz guards at routes and services; never substitute
  token RoleName or an allow-all test fallback. Unknown 500 errors must return
  literal `"internal server error"`; `err.Error()` is for mapped domain 4xx only.
- Register audit actions in `internal/audit/constants.go`; existing values persist
  in storage. Preserve worker idempotency and Kafka key formats; read landmines first.
- Only DRAFT notifications may be edited/deleted. CANCELLED is schema-only;
  implementing cancellation is new work.

## Conventions

Commit directly to `main`; linear history, no PR/branch workflow. Use scoped
Conventional Commits. Copy the nearest feature layout; staff/segments repository
interfaces live in `interfaces.go`. Update API docs with new endpoints and schema
rationale in `docs/database.md`.
