# CI readiness

Tanggal: 2026-11-02. Scope: step 18, static/unit/dependency checks.
Workflow: [Backend CI](../../.github/workflows/ci.yml).
Local runner: [scripts/ci.sh](../../scripts/ci.sh).

## Execution boundary

GitHub Actions dipilih karena origin repo berada di GitHub. Workflow berjalan
untuk push `main`, pull request menuju `main` (opsional; tidak memaksa workflow
PR), manual dispatch dan jadwal mingguan. GitHub execution belum diamati karena
perubahan belum di-push. Repo Actions/ruleset/required checks belum diperiksa
atau diubah; membuat YAML tidak otomatis menegakkan branch protection.

Quality dan vulnerability adalah job independen dengan timeout 20 menit.
Checkout memakai `persist-credentials: false`, permission token `contents: read`,
dan action resmi terpin ke SHA; versi Go mengikuti `go.mod`. Tidak ada deploy,
database/Kafka service, pengiriman push, secret produksi atau pembacaan `.env`.
Makefile tetap memiliki `include .env`; checks memanggil script langsung.

## Gates dan batas bukti

| Gate | Command | Yang dibuktikan |
| --- | --- | --- |
| Format | `bash scripts/ci.sh format` | Seluruh tracked Go files termasuk generated code sesuai gofmt toolchain terpilih; tidak menulis file |
| Modules | `bash scripts/ci.sh modules` | Download/checksum dependency valid dan go.mod/go.sum tidak berubah terhadap commit |
| Build | `bash scripts/ci.sh build` | Seluruh packages/binaries compile dengan module mode readonly |
| Unit tests | `bash scripts/ci.sh test` | Unit tests existing lulus; belum integration/race/load/device tests |
| Vet | `bash scripts/ci.sh vet` | Analisis default go vet lulus; bukan semua bug/security findings tertutup |
| Vulnerability | `bash scripts/ci.sh vuln` | govulncheck v1.9.0 menganalisis default-build code + unit tests terhadap database Go saat scan; custom integration tag tidak diaktifkan; reachable finding atau kegagalan scanner memberi exit nonzero |

Scanner dipasang ke temporary directory lalu dibersihkan; versi scanner tetap,
database advisory tetap diperbarui. Temuan imported/required tetapi tidak
reachable dapat dilaporkan sebagai informasi; exit sukses bukan klaim semua
dependency bebas vulnerability. Jika database/network tidak tersedia, scan
gagal, tidak diganti hasil sukses. Tidak ada suppression/continue-on-error.

## Baseline vulnerability dan remediation

Scan awal dengan Go 1.26.4 memberi exit 3: **24 reachable findings** pada standard
library dan tiga module (`x/net`, `x/text`, gRPC). Ini hasil static reachability,
bukan bukti exploit di production. Contoh advisory dan perbaikan:

| Komponen | Sebelum | Sesudah yang diuji | Sumber primer contoh |
| --- | --- | --- | --- |
| Go | 1.26.4 | 1.26.10 | [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) |
| golang.org/x/net | v0.57.0 | v0.60.0 | [GO-2026-6617](https://pkg.go.dev/vuln/GO-2026-6617) |
| golang.org/x/text | v0.40.0 | v0.42.0 | [GO-2026-6629](https://pkg.go.dev/vuln/GO-2026-6629); versi v0.42.0 diperlukan x/net v0.60.0 |
| google.golang.org/grpc | v1.82.0 | v1.83.1 | [GO-2026-6061](https://pkg.go.dev/vuln/GO-2026-6061), [GO-2026-6348](https://pkg.go.dev/vuln/GO-2026-6348) |
| go.opentelemetry.io/otel/sdk | v1.44.0 | v1.45.0 | [GO-2026-6505](https://pkg.go.dev/vuln/GO-2026-6505) |

Go module resolution juga memperbarui crypto/mod/sync/sys/term/tools sesuai
dependency requirements; OpenTelemetry core/metric/trace mengikuti SDK family.
Scan antara masih memberi dua reachable findings pada SDK/gRPC, sehingga versi
final di tabel memakai fixed versions untuk kedua advisory tersebut juga.
`go.sum` disimpan bersama `go.mod`; README dan dev
image minimum Go diselaraskan. Tidak ada major-version migration atau perubahan
API aplikasi. Image Docker belum dibangun/diverifikasi oleh langkah ini.

## Verification status

Validasi lokal setelah remediation memakai Go 1.26.10, govulncheck v1.9.0,
`GOCACHE=/tmp/hello-go-review-cache` dan
`GOMODCACHE=/tmp/hello-ci-modcache` pada darwin/arm64:

- Format, module verification, build, seluruh unit tests dan vet lulus.
- Scan akhir exit 0: **0 reachable vulnerabilities**; masih ada 1 advisory pada
  imported package dan 3 pada required modules yang tidak terjangkau menurut
  scanner. Ini bukan hasil nol seluruh advisory atau security sign-off.
- `bash -n scripts/ci.sh` dan actionlint v1.7.12 terhadap workflow lulus.
- `git diff --check` lulus. Tidak ada hosted GitHub run, Linux runner, Docker
  image build, integration/provider/load/device/restore test yang telah dibuktikan.

Step 18 selesai sebagai konfigurasi dan verifikasi lokal; hosted run dan aturan
repository tetap perlu diperiksa setelah push yang diotorisasi pengguna.

Referensi konfigurasi: [checkout](https://github.com/actions/checkout),
[setup-go](https://github.com/actions/setup-go),
[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck).
Pin action dan versi scanner diverifikasi terhadap metadata resmi GitHub/Go.
API contract lint dan Postman collection runs ditambahkan ketika artifact dan
isolated integration target tersedia; tidak ada Postman workspace yang dibuat
atau dipublikasikan oleh step ini.
