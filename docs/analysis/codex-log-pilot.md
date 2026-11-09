# Optional Go test log summary pilot — 2026-11-03

Ordinary `go test` remains the default. The pilot found essentially no output
savings over its already quiet successful output. No model/reasoning, global
configuration, CI, application source, or existing test command was changed.
RTK/proxies were not installed; these results do not justify their overhead.

## Use for verbose diagnostics

```bash
python3 scripts/quiet-go-test.py -- -mod=readonly ./internal/features/notifications/...
# Select a toolchain explicitly when PATH Go is older than go.mod:
python3 scripts/quiet-go-test.py --go /path/to/go -- -run TestName ./internal/token/...
```

The helper runs `go test -json` with the supplied arguments and current directory
and environment; it does not change package selection, assertions, caches, or
test execution. Python 3 standard library only. Do not use it for interactive
commands, streamed progress, benchmarks, or when full live output is needed.
It waits for command completion before displaying results.

Complete combined stdout/stderr is saved in a unique ignored
`tmp/codex-logs/go-test-*/full.log`. The printed absolute path is the retrieval
handle. `summary.txt` and `metrics.json` are saved alongside it. These logs remain
local; they can contain whatever the tests print. No automatic deletion occurs.
Read relevant ranges with `rg -n` and `sed -n`; retrieve all diagnostics when
needed. Do not delete a log while its diagnostic task is still active.

Successful test chatter is suppressed, like ordinary nonverbose Go tests.
Failed packages retain **all** their Output events, including setup and passing
test output in those packages. Skipped-test reasons are retained. Non-JSON build
errors, unknown event types, incomplete results, or exit/result disagreement
cause full raw output to be shown. A reporting exception also exposes full
output. There is no diagnostic truncation or hard output cap.

The Go command's normal exit code is returned. Signal termination maps to the
shell convention `128 + signal`, with the original negative subprocess status
recorded in metrics. A missing executable reports its error and returns 127.
Wrapper setup/storage failures can themselves fail and must not be treated as
successful test evidence. Original stdout/stderr interleaving is retained as one
combined stream; the streams are not saved separately.

## Measured results and limits

The same eight packages selected by six patterns were tested with the cached
Go 1.26.10 toolchain, `-mod=readonly -count=1`, and writable `/tmp` GOCACHE:
authz, notifications, segments, staff, middleware, and token. Both ordinary and
JSON runs exited zero. The JSON run recorded 185 passing test/subtest events,
zero failed/skipped tests, and two mock packages with no tests.

| Output | UTF-8 bytes |
|---|---:|
| Ordinary Go test, actual run | 358 |
| Full JSON log, actual run | 264,577 |
| Verbose equivalent, all Output fields from that JSON log | 67,252 |
| Final summary, including retrieval path | 353 |

The final summary was replayed from the same saved JSON after removing redundant
successful-package lines. Its reduction is **99.48% versus verbose text**, but
only **1.40% versus ordinary Go test**. JSON framing is not a realistic ordinary
test-output baseline. File paths and timings affect these small byte counts.

Ordinary and JSON run wall times were 18.053 s and 4.505 s; the first warmed build
caches, so this is **not evidence of a speedup**. No model requests or matched
agent-task A/B were run. Total input/output/cached/reasoning tokens, task retries,
and implementation quality differences were not measured. Output-byte reduction
must not be advertised as total-session token savings.

Six wrapper checks cover failed-package setup/assertions, skip reasons, raw
compiler errors, unfamiliar/incomplete output, status disagreement, exact exit
37/log retention, and SIGTERM/143 handling. Run:

```bash
python3 -B -m unittest discover -s scripts -p test_quiet_go_test.py
```

Evidence and pre-edit instruction/report backups are in ignored
`tmp/codex-log-pilot/`; the raw log path is in `final-metrics.json`. No existing
application source changes were needed. Future full-task comparisons should
keep model, effort, starting source, task, and checks constant and count recovery
reads as well as initial output. Next optimization should follow whichever
component actual task telemetry identifies as dominant.
