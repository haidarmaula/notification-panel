# Go engineering standards

Apply this profile to handwritten Go code, tests, comments and documentation.
Adoption does not certify that all existing code already complies. The pinned
[Go skills](tooling/go-skills.md) provide detailed guidance; this profile explicitly
supersedes conflicting community defaults. User instructions, correctness,
security and repository instructions take precedence. Load relevant skills and
references on demand rather than the entire collection for each task.

## Design and readability

- Prefer simple, focused functions, clear ownership and a readable normal path.
  Keep abstractions tied to actual needs and failures explicit.
- Retain module/import prefix `hello`, the current feature layout and manual
  constructor injection. Architecture changes, module renaming and DI frameworks
  require a separate justified change.
- Follow Go naming conventions and familiar domain terms. Boolean names must
  read clearly; an `is`/`has`/`can` prefix is not mandatory. Persisted audit labels,
  SQL names and public JSON identifiers remain contracts, even if their naming
  differs from new Go conventions.
- Prefer small consumer-owned interfaces when substitution is useful. Do not
  mechanically add interfaces, generics, functional options or configuration
  structs to satisfy a numerical parameter-count rule.
- Use named fields in nontrivial struct literals. Break long calls and conditions
  at semantic boundaries. Line length and condition complexity are review signals;
  preserve short-circuit evaluation and resource lifetimes when simplifying.
- Prefer the standard library and existing dependencies. Skill examples and tool
  prerequisites do not authorize installations. Dependency additions/upgrades
  must be deliberate, in scope and reviewed; never run broad `go get -u` cleanup.

## Values and generated code

- Nil slices are valid internal zero values; allocate maps before writes. Define
  and test `null` versus `[]` at API boundaries instead of allocating every slice.
- Use concrete types where practical, interfaces where useful, and `any` at real
  heterogeneous-data, JSON or logging boundaries. Generics must improve type safety
  or remove meaningful duplication.
- Preserve existing repository interface locations and mock generation. Never
  hand-edit SQLC or generated mocks: edit SQL/interfaces, create paired migrations
  before sqlc generation, and retain generated output. Community formatting and
  comment rules exclude generated and vendored source.

## Errors, security and state

- Add diagnostic operation context to errors where useful; retain
  `errors.Is`/`errors.As` matching. Avoid repetitive wrapping and repeated logging
  of the same propagated error.
- Keep tokens, proofs, credentials and sensitive payloads out of logs, errors and
  public DTOs. Unknown HTTP 500 errors remain literal `internal server error`;
  only mapped domain 4xx responses may expose approved messages.
- Preserve current database-backed authority, explicit project grants, fail-closed
  policy and immutable application/owner attribution. Never substitute token role
  names, cached authority or permissive test fallbacks.
- Commit critical mutations and required audit/outbox evidence atomically. Preserve
  lock order, isolation, deadlines, idempotency namespaces and lineage/generation
  fences; review implicit foreign-key and unique-conflict locks too.
- Retry only understood effects. A database rollback cannot undo broker/provider
  I/O, so transactions containing those effects must not be blindly retried.
- Handle cleanup errors by consequence. An explicitly ignored rollback after
  commit can be appropriate; meaningful close, flush, durability and security
  failures must be handled. Do not turn harmless `ErrTxClosed` cleanup into a
  failed business result.

## Concurrency and resources

- Pass context first, propagate cancellation, and bound external calls, retries,
  fan-out, memory and transactions. Use database time for guarded leases/deadlines
  where the protocol requires it.
- Make goroutine, channel, connection and transaction ownership explicit. Cancel
  and join background work on shutdown or assignment loss. Do not hide unbounded
  goroutines behind timeout wrappers.
- Load configuration once and keep it immutable. Avoid mutable globals and hidden
  initialization side effects. Measure before optimizing; local fixtures and
  simulations do not establish production capacity or device delivery.

## English documentation and comments

- Maintained documentation, Godoc and explanatory comments must be in English.
  Preserve personal names, technical identifiers and historical conversation
  records. Translating documentation does not authorize changing runtime wire
  messages or a product's localisation contract.
- Document exported packages, types and functions concisely. Explain purpose,
  invariants, ownership, cancellation, failures and unusual tradeoffs when useful;
  avoid comments that merely repeat declarations.
- Do not require Parameters/Returns/Example boilerplate on every function. Add
  runnable examples for non-obvious contracts. Never invent rationale, benchmark
  claims, examples or Playground links.
- Follow existing `docs/adr/`, `docs/specs/` and `docs/api/` conventions and retain
  decision history. Preserve proposed/implemented/local-tested/production-verified
  distinctions, open gates and requirement strength. Preserve limits, commands,
  paths and contract literals when editing prose; check incoming heading links.
- This private backend does not require badges, a public documentation site,
  `llms.txt`, a new API generator or public registry submission merely because a
  general-purpose skill recommends them. Publishing private code or metadata
  requires explicit authorization.

## Verification and review

- Start with focused behavior tests; broaden for shared contracts, SQL, composition
  and concurrency. Test observable invariants and failure paths. Coverage measures
  execution, not correctness.
- Use isolated opt-in integration fixtures. Do not add `t.Parallel()` to shared
  database/Kafka fixtures without proving isolation. Individual and table-driven
  tests are both valid; choose useful names and failure reports.
- Required CI checks remain formatting, module verification, build, unit tests,
  vet and reachable vulnerability scanning. Unit CI uses `-race`; the pinned skill
  snapshot is checked offline for drift. Never skip tests, weaken assertions or
  suppress scanner/network failures to obtain a green result.
- Additional linters, coverage thresholds and leak-checking dependencies need a
  separate measured rollout. This adoption does not claim they are enforced.
- Review clarity, correctness, security, concurrency, tests and operational behavior.
  Use parallel agents when an applicable workflow calls for them and independent
  work justifies the cost; avoid duplicated context and reviews.
- Commit coherent changes directly to `main` with scoped Conventional Commits.
  Preserve unrelated user work; do not introduce an unsolicited branch/PR/push flow.

## Adoption workflow

For each change, identify its boundary, read the applicable skills, implement a
focused slice, review the diff and run appropriate checks. Update matching API,
schema or operational docs. Fix touched violations where safe and record broader
issues separately. The next code-quality audit should start with modern
`internal/dispatch` and `internal/installation`, then address other packages by risk.
