# Dependency integration evidence

Tanggal: 2026-11-02. Step 19/M01 dependency foundation, diuji lokal pada
darwin/arm64 + Docker Desktop. [SPEC](../specs/integration-environment.md),
[runner](../../scripts/integration.sh), [Compose](../../compose.integration.yml),
[tests](../../tests/integration/dependencies_test.go).

## Menjalankan ulang

Prasyarat yang diuji: Go 1.26.10, Docker 28.1.1 dengan daemon aktif, Compose
2.35.1, golang-migrate CLI v4.19.1, Postman CLI 1.75.0 dan Python 3. Image
PostgreSQL/Kafka dipin dengan registry digest; pull memerlukan network bila
image belum tersedia. Tidak perlu login/workspace/key Postman untuk local run.

```bash
bash scripts/integration.sh
```

Di sandbox sesi ini, Go memakai GOCACHE=/tmp/hello-go-review-cache dan
GOMODCACHE=/tmp/hello-ci-modcache. Docker socket/localhost membutuhkan izin
runtime di luar sandbox. Ini tidak mengubah kebutuhan credential aplikasi.

Runner memakai project `hello-integration`, port loopback 15432/19092/14010,
fixture DB/user/password publik, temporary PostgreSQL/Kafka storage, dan
temporary simulation logs. `.env` development tidak dimuat oleh Compose
(`--env-file /dev/null`) maupun application config loaders. Tidak ada server,
worker atau scheduler aplikasi yang dijalankan. File compose development dan
Makefile tidak diubah. Bridge terpisah bukan outbound firewall; Firebase test
transport membatasi setiap request ke simulation loopback dan menolak redirect.

Lock mencegah run paralel. Jika project/port sudah dipakai, runner menolak;
tidak mengambil alih atau menghapus resource yang sudah ada. Cleanup hanya
project yang baru dibuat runner dan PID simulasi miliknya, tanpa Docker prune
atau menghentikan Docker Desktop. Setelah hard crash, inspect project/process
dan lock yang dilaporkan sebelum membersihkan fixture secara manual.

## Hasil yang sudah diamati

| Pemeriksaan | Hasil / batas bukti |
| --- | --- |
| Image aktual | PostgreSQL 17.10 dan Kafka 4.3.1; digest ada di Compose; RF1/1 partition, bukan HA |
| Migrations | Seluruh 15 migration up → down-all → up lulus pada fixture DB baru |
| Repository aktual | database.New + generated sqlc + UserRepository Create/FindByExternalID dan empat seed roles lulus |
| Producer aktual | Kedua topic diprovisikan eksplisit; NewProducer menerbitkan event dan reader memperoleh key serta JSON yang sama |
| Firebase SDK aktual | Send terhadap fixture accepted, UNREGISTERED, quota 429, unavailable 503 menghasilkan response/error classification yang diharapkan |
| Deadline | Caller berhenti sekitar 100 ms, tidak menunggu response tertunda 2 detik |
| Egress test | Request URL provider nyata ditolak local transport sebelum network I/O; tidak ada credential/real device digunakan |
| Simulation log | 9 actual SDK POST requests; status 200×2, 404×1, 429×1, 503×5; termasuk delayed response setelah caller timeout |
| Guard | Port sibuk ditolak; existing project container dipertahankan setelah refusal; unknown argument exit 2; tag tanpa explicit opt-in gagal, tidak skip |
| Cleanup | Success dan test-failure paths menghapus fixture containers/network; pre-existing-container case tidak menghapus container tersebut |
| Checks | Ordinary go test ./... tetap lulus, tagged go vet lulus; Bash/JS syntax dan Compose config divalidasi |

SDK menghasilkan lima physical HTTP requests untuk satu logical Send saat 503
karena retry internal. Ini bukti skenario fixture, bukan janji timing provider
nyata. Executor/rate/attempt design M06 harus memperhitungkannya; step ini tidak
mengubah SDK retry policy atau adapter production.

Smoke suite lengkap lulus dengan exit 0. Log runtime mentah ditempatkan di
temporary directory dan dihapus, tidak di-commit; runner memeriksa request
events agar sukses tidak hanya membuktikan mock berhasil startup. Context
simulation menyimpan provenance dan ketidakpastian untuk run berikutnya.

## Batas integrasi dan tindak lanjut

Owner mock lookup tidak tersedia karena Postman CLI signed out; tidak ada klaim
mock provider tidak ada. Stand-in lokal dibuat dari Firebase REST send/error
documentation dan pinned Go SDK, lalu diperketat untuk fixture requests.
CLI 1.75 membutuhkan `path` manifest file, bukan directory. Mock/simulation
tidak dipush, dideploy atau dipublikasikan ke workspace.

FCMProvider sekarang memakai SendAll, worker masih OneSignal. Harness memakai
Firebase SDK Send langsung, **bukan** adapter/worker aplikasi. Belum dibuktikan:
admission/outbox/leases, rebalance/offset safety, multi-partition affinity,
provider selection/credential, token enrollment/receipt, real FCM/APNs/device,
20k throughput, multi-host failure atau restore. Linux/GitHub-hosted execution
dan automatic integration job belum dijalankan oleh step ini.

M01 selesai sebagai checks + lingkungan lokal yang dapat diulang. Berikutnya
step 20: SPEC slice M02 permission/API baseline; G5 perlu settled untuk policy
yang diimplementasikan. Tambahkan integration/failure cases setiap milestone,
bukan menganggap dependency smoke sudah membuktikan seluruh backend reliable.
