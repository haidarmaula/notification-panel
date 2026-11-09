# Codex efficiency setup — 2026-11-03

Applied locally; application source was not changed. These measurements describe
this setup, not a claim of equivalent implementation quality at lower effort.

Follow-up: [optional Go test log pilot](codex-log-pilot.md) retains full diagnostic
logs. It produced large savings over verbose output but negligible savings over
ordinary Go test; it is not enabled as the default test path. The instruction
byte measurements below describe the original setup, before that small addition.

## Settings and files

| File | Change |
|---|---|
| `~/.codex/config.toml` | `model_reasoning_effort`: `high` → `medium`; `model_verbosity`: unset → `low` |
| `~/.codex/AGENTS.md` | New compact navigation, context reuse, output, testing, and checkpoint defaults |
| `~/.codex/deep.config.toml` | New explicit high-effort CLI profile |
| `AGENTS.md` | Compact entry points, safety invariants, and required reading triggers |
| `docs/agent-reference.md` | Original commands, environment, architecture, and landmines preserved verbatim |
| This file | Audit, measurements, validation, and activation limitations |

Model remains `gpt-6.1-sol`. Its cached catalog advertises low, medium, high,
xhigh, max, and ultra efforts and supports verbosity; its output default was
already low. Explicit low verbosity therefore makes the preference durable but
has no demonstrated savings here. Medium is a conservative engineering default;
no model change, automatic effort selector, or automatic delegation was enabled.

All other config values compare equal, including plugins, MCP, project trust,
notification hook, service tier, and desktop preferences. Sandbox, approval,
network, authentication, and hook-trust settings were not changed. Credentials
were not read. No tools or dependencies were installed.

## Audit and compatibility

- Desktop metadata: `26.1027.51404`; bundled CLI:
  `0.166.0-alpha.5.1`; PATH CLI: `0.154.0`. Validation used the bundled CLI.
- The active chat records `gpt-6.1-sol`/high; saved defaults do not prove that an
  existing chat's effort has changed. No Desktop state database was edited.
- No preexisting global AGENTS/override, ancestor override, project config, or
  user-created filesystem skills were found in the inspected locations.
- Configured plugins include Postman, app tools, browser/computer use, visualize,
  and document/spreadsheet/presentation tools. Node REPL MCP is configured.
  These remain enabled to preserve capability; their prompt overhead remains.
- The old root guide was 12,801 UTF-8 bytes and repeatedly loaded specialized
  provider/integration details. It is now 3,626 bytes with explicit reading
  triggers. Its obsolete “no CI” claim was corrected against the workflow.
- Go module `hello` has four entry points, seven HTTP feature modules, PostgreSQL
  via pgx/sqlc, Kafka, and OneSignal worker delivery. Existing detailed guidance
  was relocated rather than replaced with another architecture document.
- Native `config/read` with strict validation recognizes medium effort and low
  verbosity. Global then repository instructions appear in the native prompt;
  the task reference is not automatically preloaded. No overrides mask them.
- Native CLI accepts `--profile deep debug prompt-input`; the profile parses as
  high effort. The debug output does not independently expose runtime effort.
  `--profile` is unsupported on app-server, and `--strict-config` is unsupported
  on debug commands; those combinations were not treated as successful checks.

## Measurements

Equivalent offline `debug prompt-input` snapshots used the same workspace and
prompt: “Locate the worker provider constructor and report the path and symbol
only.” No model request or paid A/B implementation benchmark was run.

| Proxy | Before | After | Reduction |
|---|---:|---:|---:|
| Loaded instruction text, UTF-8 bytes | 12,898 | 5,493 | 57.41% |
| All text parts in native prompt, UTF-8 bytes | 57,812 | 50,407 | 12.81% |
| Serialized native prompt JSON, bytes | 96,418 | 88,891 | 7.81% |

The after instruction measurement includes the new global guidance. JSON bytes
include formatting and metadata. None of these percentages measures tokens,
cached billing, model execution time, or end-to-end task savings. Tasks requiring
the reference will reload relevant detail and realize smaller context savings.

Actual telemetry is available in local session JSONL `token_usage_record` events
and aggregate thread state. The pre-change audit snapshot at 08:00:28 Jakarta
recorded 573,600 input, 501,248 cached input, 6,029 output, 1,406 reasoning-output,
and 579,629 total tokens. These are cumulative audit usage, not a controlled
task baseline or evidence of savings. Cached input is part of input; reasoning
output is part of output. No matched post-change inference run exists, so actual
token savings, runtime savings, retry rate, and quality differences are unknown.
No new telemetry export or analytics was enabled.

## Validation and activation

- TOML parsing and native strict base-config reading passed. Structural comparison
  permits only the two documented setting changes. Persistent backups exist.
- Detailed reference sections match the original byte-for-byte. SHA-256 comparison
  of all 241 originally tracked files found only `AGENTS.md` changed.
- `bash scripts/ci.sh format` and `git diff --check` passed.
- Focused `go test -mod=readonly ./internal/token/...` passed with the already
  cached Go 1.26.10 toolchain and writable `/tmp` build cache (2.185 s wall time).
  PATH Go 1.26.4 with `GOTOOLCHAIN=local` first rejected the required version;
  no download or version downgrade was used. Full/integration suites were not run
  for this documentation/configuration change. This is limited smoke evidence.

Fresh CLI invocations load the changes, as verified. Start a fresh Desktop chat
to use new instructions/defaults; an existing chat or saved composer selection
may retain high effort. No app restart has been demonstrated necessary; restart
only if a new chat retains stale settings. No pending approval remains.

Use medium for ordinary engineering work. For a routine bounded task, explicitly
select low using Desktop model controls or CLI
`codex -c 'model_reasoning_effort="low"'`. For security, migrations, concurrency,
architecture, broad refactoring, repeated failures, or uncertainty, select high
in Desktop or run `codex --profile deep` (equivalently an explicit high override).
These are manual choices; instruction text does not change API effort.

Backups and machine-readable evidence are under
`~/.codex/backups/token-efficiency-20261103/` (private directory). It contains the
original config/root guide, absence manifest for new files, tracked-file hashes,
prompt snapshots, telemetry snapshot, metrics, and validation results. Restore
only after checking for subsequent edits; do not blindly overwrite later settings.

Supported configuration and instruction behavior were checked against the
[official configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference),
[AGENTS guidance](https://learn.chatgpt.com/docs/agent-configuration/agents-md), and
[profile documentation](https://learn.chatgpt.com/docs/config-file/config-advanced).
