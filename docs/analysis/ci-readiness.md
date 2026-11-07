# CI readiness

Date: 2026-11-02. Scope: step 18, static/unit/dependency checks.
Workflow: [Backend CI](../../.github/workflows/ci.yml).
Local runner: [scripts/ci.sh](../../scripts/ci.sh).

## Execution boundary

GitHub Actions was selected because the repo origin is on GitHub. The workflow runs
on pushes to `main`, pull requests targeting `main` (optional; this does not mandate a
PR workflow), manual dispatch and a weekly schedule. GitHub execution has not been observed because
the changes have not been pushed. Repo Actions/rulesets/required checks have not been inspected
or changed; creating YAML does not automatically enforce branch protection.

Quality and vulnerability are independent jobs with 20-minute timeouts.
Checkout uses `persist-credentials: false`, token permissions are `contents: read`,
and official actions are pinned to SHAs; the Go version follows `go.mod`. There is no deployment,
database/Kafka service, push delivery, production secret use or `.env` reading.
The Makefile retains `include .env`; checks invoke the script directly.

## Gates and evidence limits

| Gate | Command | What it establishes |
| --- | --- | --- |
| Format | `bash scripts/ci.sh format` | All tracked Go files, including generated code, conform to gofmt in the selected toolchain; no files are written |
| Modules | `bash scripts/ci.sh modules` | Dependency downloads/checksums are valid and go.mod/go.sum are unchanged from the commit |
| Build | `bash scripts/ci.sh build` | All packages/binaries compile in readonly module mode |
| Unit tests | `bash scripts/ci.sh test` | Existing unit tests pass; integration/race/load/device tests are not covered |
| Vet | `bash scripts/ci.sh vet` | Default go vet analysis passes; this does not close every bug/security finding |
| Vulnerability | `bash scripts/ci.sh vuln` | govulncheck v1.9.0 analyzes default-build code + unit tests against the Go database at scan time; custom integration tags are not enabled; reachable findings or scanner failures produce a nonzero exit |

The scanner is installed in a temporary directory and then cleaned up; its version is fixed,
while the advisory database continues to update. Imported/required but
unreachable findings may be reported as information; a successful exit does not claim all
dependencies are vulnerability-free. If the database/network is unavailable, the scan
fails rather than returning a substitute success. There are no suppressions/continue-on-error.

## Baseline vulnerabilities and remediation

The initial scan with Go 1.26.4 returned exit 3: **24 reachable findings** in the standard
library and three modules (`x/net`, `x/text`, gRPC). This is static reachability,
not evidence of production exploitation. Example advisories and fixes:

| Component | Before | Tested after | Example primary source |
| --- | --- | --- | --- |
| Go | 1.26.4 | 1.26.10 | [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) |
| golang.org/x/net | v0.57.0 | v0.60.0 | [GO-2026-6617](https://pkg.go.dev/vuln/GO-2026-6617) |
| golang.org/x/text | v0.40.0 | v0.42.0 | [GO-2026-6629](https://pkg.go.dev/vuln/GO-2026-6629); x/net v0.60.0 requires v0.42.0 |
| google.golang.org/grpc | v1.82.0 | v1.83.1 | [GO-2026-6061](https://pkg.go.dev/vuln/GO-2026-6061), [GO-2026-6348](https://pkg.go.dev/vuln/GO-2026-6348) |
| go.opentelemetry.io/otel/sdk | v1.44.0 | v1.45.0 | [GO-2026-6505](https://pkg.go.dev/vuln/GO-2026-6505) |

Go module resolution also updated crypto/mod/sync/sys/term/tools according to
dependency requirements; OpenTelemetry core/metric/trace follow the SDK family.
An intermediate scan still returned two reachable findings in SDK/gRPC, so the
final versions in the table also include fixes for those two advisories.
`go.sum` is retained alongside `go.mod`; the README and minimum Go version in the dev
image were aligned. There is no major-version migration or application
API change. The Docker image was not built/verified by this step.

## Verification status

Local validation after remediation used Go 1.26.10, govulncheck v1.9.0,
`GOCACHE=/tmp/hello-go-review-cache` and
`GOMODCACHE=/tmp/hello-ci-modcache` on darwin/arm64:

- Format, module verification, build, all unit tests and vet passed.
- The final scan returned exit 0: **0 reachable vulnerabilities**; 1 advisory remains in
  an imported package and 3 in required modules that the scanner considers unreachable.
  This is not a zero-advisory result or security sign-off.
- `bash -n scripts/ci.sh` and actionlint v1.7.12 against the workflow passed.
- `git diff --check` passed. No hosted GitHub run, Linux runner, Docker
  image build, integration/provider/load/device/restore test has been demonstrated.

Step 18 is complete as configuration and local verification; hosted runs and
repository rules still need inspection after a user-authorized push.

Configuration references: [checkout](https://github.com/actions/checkout),
[setup-go](https://github.com/actions/setup-go),
[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck).
Action pins and the scanner version were verified against official GitHub/Go metadata.

## Backend release checks added on November 8

The quality job checks all mounted route registrations, required staff permissions,
three OpenAPI documents, two event schemas and their synthetic examples. The
delivery job exercises two 32-device broadcasts in an isolated Docker network.
Local execution of the CI command produced 66 charged calls, 64 accepted
delivery results, 64 unique Kafka updates and an empty outbox after transient
503 responses. Manual workflow dispatch also builds and [checks the six
production images](../runbooks/production-images.md). The complete opt-in
Postman/DB/Kafka/race and physical recovery suites run separately. Hosted
execution and repository branch rules still require target evidence.
