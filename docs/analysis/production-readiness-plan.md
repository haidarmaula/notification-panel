# Backend production readiness plan

Tanggal: 2026-11-02 (Asia/Jakarta). Status: perancangan aktif, belum siap produksi.

Dokumen ini adalah indeks pekerjaan dan kebutuhan terbaru. ADR tetap berstatus
`proposed` sampai keputusan dan review selesai. Checklist implementasi tidak
boleh dianggap selesai hanya karena rancangan atau file kode tersedia.

## 1. Kebutuhan yang sudah dikonfirmasi

| ID | Kebutuhan | Status / konsekuensi |
| --- | --- | --- |
| R1 | Repository ini hanya backend; portal admin di repository lain | Dikonfirmasi pengguna. Kontrak API termasuk scope, implementasi UI tidak. |
| R2 | Sekitar 20.000 perangkat aktif untuk konteks awal | Dikonfirmasi pengguna; ini bukan kapasitas per aplikasi atau kapasitas total multi-aplikasi yang sudah divalidasi. Jumlah aplikasi/aggregate peak belum ditentukan. |
| R3 | Sekitar 5 campaign per bulan | Dikonfirmasi pengguna; bukan 100 campaign/detik. |
| R4 | Broadcast terbesar menjangkau seluruh user aktif pada aplikasi yang dipilih | Dengan kebutuhan multi-aplikasi, broadcast tidak boleh melintasi batas aplikasi; fan-out menghitung perangkat eligible dalam scope tersebut. |
| R5 | OneSignal akan diganti dengan FCM | Dikonfirmasi pengguna; fallback OneSignal permanen tidak menjadi kebutuhan. |
| R6 | Android dan iOS didukung | Dikonfirmasi pengguna. |
| R7 | Backend mengirim melalui FCM untuk kedua platform; iOS melalui APNs di sisi Firebase | Dikonfirmasi pengguna. Provider APNs langsung di backend tidak dibutuhkan untuk versi pertama. |
| R8 | Broadcast diinginkan sangat cepat | Dikonfirmasi sebagai arah kebutuhan; belum ada batas waktu numerik yang disepakati. |
| R9 | Aplikasi mobile memakai React Native | Dikonfirmasi pengguna; versi framework/native/library belum diverifikasi. |
| R10 | Kafka akan di-deploy di server perusahaan sendiri (self-hosted) | Dikonfirmasi pengguna; jumlah host/failure domain/resource dan operator belum diketahui. |
| R11 | V1 fokus broadcast/segment/individual, scheduling, recovery, receipt/statistik, keamanan dan operasi | Dikonfirmasi pengguna; UPLOAD, cancel/resubmit, rich media serta preferences/quiet hours ditunda. |
| R12 | VIEWER read-only; OPERATOR draft/segment; send/schedule hanya ADMIN/SUPER_ADMIN; administrasi staff/role hanya SUPER_ADMIN | Dikonfirmasi pengguna 2026-11-02. SPEC M02a siap untuk subset notification/segment/member; guard M02a sudah diterapkan/diuji lokal; staff administration M02b juga implemented/local-verified; security lain tetap pending. |
| R13 | Backend generik untuk banyak aplikasi mobile, bukan khusus kaiapps2 | Dikonfirmasi pengguna 2026-11-03. [ADR-12](../adr/adr-12-multi-application-isolation.md) dan [rencana M03](../specs/m03a-application-scope-plan.md) proposed; runtime/schema saat ini belum mengisolasi aplikasi. |

Pengguna memperkirakan project Firebase sudah ada karena dipakai pada integrasi
OneSignal. Itu menjadi titik awal inventory, **bukan** konfirmasi project ID,
akses, app Android/iOS, APNs atau izin pengiriman backend sudah siap.

Dokumen lama tetap berguna sebagai riwayat, tetapi asumsi 100 campaign/detik,
burst 1.000/detik, pool 32, dan estimasi 8,6 juta idempotency key/hari bukan
requirement terbaru. Konfigurasi harus dikalibrasi terhadap fan-out perangkat
dan kuota provider, bukan disalin dari asumsi tersebut.

Jika kelima campaign masing-masing menjangkau 20.000 perangkat, estimasi
dasarnya sekitar 100.000 pengiriman perangkat/bulan sebelum retry. Ini
perhitungan skenario, bukan hasil pengukuran traffic produksi.

## 2. Scope awal dan target yang masih diusulkan

Scope v1 yang dikonfirmasi: auth/staff/profile, user/device lifecycle, segment, campaign
broadcast/segment/individual, scheduling, delivery ledger, idempotency,
retry/recovery, receipt, statistik, keamanan, dan operasi produksi.

UPLOAD audience, cancel/resubmit campaign, rich media, quiet hours, dan
preferences ditunda dari v1. Localisation belum diputuskan; jangan menjanjikan
parity OneSignal. Kontrak harus menolak input fitur yang belum didukung secara
eksplisit. Emergency dispatch hold, dedup/idempotency, token/logout lifecycle,
rate control dan audit tetap bagian reliability/security inti, bukan fitur
parity tambahan. Hold menghentikan klaim baru; tidak menarik kembali push yang
sudah diajukan ke provider.

Pengguna menyatakan repo React Native tersedia (lokasi belum diberikan).
Laravel berjalan langsung di server dan repository-nya tidak tersedia.
Verifikasi kontrak auth tetap terbuka; jangan menganggap inspeksi/deployment
server Laravel telah diizinkan atau menjadikan akses server sebagai syarat
memulai perbaikan baseline backend.

Usulan benchmark awal (belum SLA dan belum disetujui): fan-out 20.000 perangkat
eligible dalam <=60 detik dari durable queue acceptance sampai seluruh hasil
submission provider tercatat, saat sistem dan provider sehat. Kegagalan token
permanen dihitung sebagai hasil eksplisit; retry gangguan provider diukur
terpisah. Ini memerlukan rata-rata sekitar 334 hasil perangkat/detik.

Angka tersebut hanya kandidat eksperimen. Load test lokal mengukur pipeline
terhadap provider simulasi; uji provider nyata hanya ke perangkat uji dengan
kuota dan pengaturan traffic yang sesuai. Hasil simulasi tidak membuktikan
kapasitas provider atau waktu tiba di ponsel. Tetapkan target final setelah
baseline benchmark dan pilot; hindari traffic spike yang tidak terkendali.

| Keputusan terbuka | Yang perlu ditentukan | Menghalangi |
| --- | --- | --- |
| SLO API, scheduling, fan-out, availability | Nilai p95/p99, jendela ukur, kondisi dan error budget | Sizing final dan go/no-go produksi |
| Infrastructure | Kafka self-hosted dikonfirmasi; host/failure domain, PostgreSQL, resource budget, staging dan TLS belum ditentukan | ADR-07/08/11 dan deployment |
| Recovery | RPO/RTO, backup, restore, operator dan alert escalation | Release gate |
| Mobile | React Native dikonfirmasi; versi/library, akses repo, jadwal update, rollout adoption dan identifier aktual belum diverifikasi | ADR-06 dan cutover |
| Lifecycle token | Login/logout/account switch, multi-device, refresh dan invalidation | Kontrak mobile dan pengujian keamanan |
| Retention | Delivery, audit, dead-letter, key, outbox, PII deletion | ADR-10 dan maintenance produksi |
| Scope tambahan | UPLOAD, cancel/resubmit, rich media, preferences/quiet hours ditunda; localisation belum diputuskan | Penolakan input dan batas kontrak API |

## 3. Inventaris working tree sebelum pekerjaan ini

Tidak ada perubahan lama yang dibuang, di-unstage, atau diklaim sebagai hasil
sesi ini. Index Git sudah berisi staged changes sebelum pekerjaan dimulai.
Perubahan processor lama sudah direkam dalam commit `bcb178c`, konfigurasi
agent lama (OpenCode, sudah tidak digunakan) dalam `51e682c`. Build lulus; test notifications/middleware/token lulus,
sedangkan test staff/segments masih gagal compile seperti baseline. Commit
preservasi ini tidak berarti kode telah memenuhi rancangan reliability baru.

| Kelompok | File | Kondisi awal dan tindak lanjut |
| --- | --- | --- |
| Kode runtime | `internal/features/notifications/processor.go` | Staged; ada perubahan perilaku guard PROCESSING, atribusi token, dan warning truncation. Perlu review tersendiri; tidak sama dengan implementasi ADR-01 lengkap. |
| Draft lama | `docs/adr/bigpickle-retry.md`, `docs/specs/bigpickle-retry.md` | Staged dan memiliki perubahan unstaged. Simpan sebagai input historis; SPEC lama bukan pasangan canonical ADR-01 terbaru. |
| Assessment | `docs/analysis/qwen38-assessment.md` | Staged; beberapa asumsi kapasitas dan klaim safety perlu direkonsiliasi. |
| ADR canonical | `docs/adr/adr-01-provider-retry.md` sampai `adr-05-receipt-ingestion.md` | Untracked, semua proposed. Pasangan SPEC canonical belum tersedia. |
| Tooling | `.opencode/agents/{architect,reviewer,tester}.md`, `opencode.json` | Staged; konfigurasi workflow, bukan fitur backend. |

Baseline verifikasi dilakukan pada turn assessment sebelumnya dalam chat ini:
`go build ./...` lulus; `go test ./...` gagal compile pada staff (signature
UpdatePassword lama) dan segments (mock CountSearch/CountBySegmentIDs belum
tersedia). Notifications, middleware, dan token lulus. Cache memakai
`GOCACHE=/tmp/hello-go-review-cache`. Ini hasil working tree saat assessment,
bukan bukti seluruh pipeline atau clean HEAD lulus. Catatan ini adalah baseline
historis sebelum implementasi test infrastructure: step 17 kemudian membuat
unit suite hijau, dan step 19 menjalankan dependency smoke nyata PostgreSQL/Kafka
serta SDK terhadap HTTP simulation. Provider/perangkat nyata belum diuji.

## 4. Indeks ADR / SPEC

Path pada tabel relatif terhadap `docs/adr/`; pasangan canonical SPEC akan
memakai basename yang sama di `docs/specs/` dan belum dibuat.

| ADR | Dokumen | Status dan pekerjaan berikutnya |
| --- | --- | --- |
| 01 | [Provider retry](../adr/adr-01-provider-retry.md) | Proposed, direvisi: atomic fan-out, per-device budget, lease/generation, recovery dan acceptance idempotency. Belum diuji. |
| 02 | [Worker consumption](../adr/adr-02-worker-consumption.md) | Proposed, direvisi: serial durable admission, fail-closed commit/DLQ, replay audit. Belum diuji. |
| 03 | [Transactional outbox](../adr/adr-03-transactional-outbox.md) | Proposed, direvisi: satu acceptance transaction dan scheduler-only publisher, satu row per bounded transaction. Belum diuji. |
| 04 | [Provider selection](../adr/adr-04-provider-selection.md) | Proposed, direvisi: explicit FCM, credential modes, typed registration, immutable binding/owner_epoch dan SDK limits. Belum diuji. |
| 05 | [Receipt ingestion](../adr/adr-05-receipt-ingestion.md) | Proposed, direvisi: device-scoped capability receipts, early/late observation, snapshot stats dan versioned event outbox. Belum diuji. |
| 06 | [Cutover/token migration](../adr/adr-06-onesignal-fcm-cutover.md) | Proposed: React Native enrollment, exclusive routing/cohorts, full-coverage gate, pilot, stage-specific rollback dan retirement; belum dieksekusi. |
| 07 | [Kafka topology](../adr/adr-07-kafka-topology.md) | Proposed: self-hosted profiles, RF/ISR, explicit hash routing, retention/groups, seed/batch config, provisioning dan ACL; belum diterapkan. |
| 08 | [Observability](../adr/adr-08-observability.md) | Proposed: private health/readiness, structured logs, metric catalog/freshness, sparse-work alerts, dashboards/runbooks dan verification; belum diterapkan. |
| 09 | [API conventions](../adr/adr-09-api-consistency.md) | Proposed: safe envelope/errors, state vocabulary, presence-aware PATCH/CAS, pagination/filter, audit/DTO dan compatibility; belum diterapkan. |
| 10 | [Retention/recovery](../adr/adr-10-retention-recovery.md) | Proposed: safety floors, controlled erasure/archive, backup/PITR, sealed restore dan source epoch; policy/RPO/RTO belum ditetapkan. |
| 11 | [Security baseline](../adr/adr-11-security-baseline.md) | Proposed keseluruhan; business role matrix dikonfirmasi dan scoped SPEC M02a ditulis. M02a operation enforcement diimplementasikan/diuji lokal; session/abuse/key/enrollment dan security lain tetap pending. |
| 12 | [Multi-application isolation](../adr/adr-12-multi-application-isolation.md) | NEW proposed: aplikasi sebagai batas identity/data/permission/provider/event/receipt; belum diimplementasikan. Optional product parity tetap backlog tanpa nomor ADR yang ditetapkan. |

Review pertama: [ADR-01/02 review](adr-01-02-review.md). Status proposed berarti
belum diterima; nama migration yang disebut ADR hanya reservasi rancangan.
Nomor aktual mengikuti urutan implementasi, selalu dengan up/down dan sqlc
regeneration. Draft lama tidak boleh menjadi instruksi implementasi paralel.

Review berikutnya: [ADR-04/05 review](adr-04-05-review.md), termasuk pertanyaan
integrasi, perubahan kontrak dan acceptance tests yang belum dijalankan.

Gate migrasi: [cutover readiness](cutover-readiness.md). Project Firebase,
versi/library React Native dan konfigurasi provider tetap perlu inventory.

Gate Kafka: [Kafka readiness](kafka-readiness.md). H47/H48/M48 adalah temuan
runtime open, bukan perbaikan yang selesai hanya karena ADR-07 tersedia.

Gate monitoring: [observability readiness](observability-readiness.md).
Tidak ada live endpoint, scrape, dashboard atau notifikasi alert yang sudah
dipasang hanya karena ADR-08/checklist tersedia.

Gate API/data/security: [readiness register](api-data-security-readiness.md).
C21 operation bypass sudah diremediasi dan diverifikasi lokal di [M02a](m02a-verification.md). Retention/RPO/RTO,
identity policy dan perubahan kontrak masih perlu keputusan/verifikasi.

Hasil review lintas ADR: [CR1–CR9 dan gate G1–G7](adr-01-11-consistency-review.md).
Koreksi teknis sudah ditulis sebagai proposed; scope inti dikonfirmasi, tetapi
SLO/TTL, integrasi, host, policy keamanan/data dan bukti cutover masih terbuka.
Urutan dependency/milestone tersedia di [M00–M10](implementation-milestones.md).
Step 16 selesai sebagai pemetaan; step 17/M00 memulihkan baseline unit test.
Step 18 menyediakan [CI checks dan bukti lokal](ci-readiness.md); hosted run
belum diamati. Step 19 menyediakan [dependency environment dan bukti lokal](integration-readiness.md).
Step 20 dimulai dengan [SPEC M02a](../specs/m02a-staff-operation-permissions.md),
[policy JSON](../contracts/staff-permissions.json) dan
[API authorization](../api/authorization-api.md). M02a implemented/local-verified;
[M02b staff administration](m02b-verification.md) juga implemented/local-verified.
[M02c audit redaction](m02c-verification.md) juga implemented/local-verified;
historical cleanup produksi tetap operator gate. [M02d request limits](m02d-verification.md)
juga implemented/local-verified. [Evaluasi baseline](m02-baseline-assessment.md)
dan [M02e staff JWT](m02e-verification.md) selesai scoped; berikutnya inspeksi
rancangan generic multi-aplikasi dan M03a registry/scope sebelum instalasi;
kaiapps2 hanya consumer uji pertama. [SPEC M03a](../specs/m03a-application-registry.md)
dan registry OpenAPI sudah ditulis. [M03a.1 persistence](m03a1-verification.md)
diimplementasikan/diuji lokal. [M03a.2 services](m03a2-verification.md) juga
local-verified. [M03a.3 HTTP](m03a3-verification.md) juga implemented/local-verified;
sepuluh endpoint registry/grants tersedia dengan PROVISIONING only. Berikutnya
M03b scope schema/query/API dan legacy pipeline sebelum data bisnis app kedua.
M03b.1–M03b.5 selesai di repo dan teruji lokal sampai commit `1b02f1f`.
[Cutover M03b](../specs/m03b-cutover.md) di lingkungan tujuan masih terbuka.
Step 20 berlanjut ke [M03c](../specs/m03c-installation-binding.md). Migrasi
000018–000019 menambahkan schema instalasi dan binding. Proteksi alamat dan
transaksi lifecycle tersedia. Verifier JWT per project dan mapping identitas
000020 tersedia. Adapter origin bertanda tangan dan proteksi replay 000021
tersedia. [API lifecycle mobile](../api/mobile-installations-api.md) dapat
dipasang lewat policy server. Dua consumer HTTP sintetis Android/iOS telah
diuji dengan PostgreSQL untuk isolasi JWT, origin, proof, konflik alamat dan
logout lintas project. Inspeksi kaiapps2 masih menunjukkan OneSignal. Verifikasi
consumer nyata dan kebijakan deployment per project tetap terbuka.
Gate integrasi dan kontrol rilis M02 tetap terbuka.

M04 acceptance/scheduling tersedia dan lulus lokal melalui migration 000022,
[kontrak transaksi](../specs/m04-durable-acceptance.md) dan
[API](../api/campaign-acceptance-api.md). Uji PostgreSQL mencakup request
serentak dengan key sama, replay saat hold, pencabutan grant, coverage legacy,
rollback audit, promotion dan expiry. Library dispatcher outbox juga lulus uji
Kafka lokal, broker tidak tersedia, dua dispatcher, seal/epoch fence dan
ack-sebelum-commit gagal. Dispatcher belum dipasang ke binary; M04 masih menunggu
admission M05 dan aktivasi scheduler yang aman. Deployment tetap sealed/held
dan tidak mengirim ke provider.

M05 atomic preparation juga tersedia sebagai library dan lulus lokal:
snapshot/count/intent lengkap dalam satu transaksi REPEATABLE READ, dedup event,
rollback utuh, hold coverage dan expiry tanpa audience palsu. Fixture 20.000
user/perangkat selesai sekitar 4 detik untuk transaksi preparation saja;
ini bukan SLA produksi atau waktu kirim FCM. Kafka serial consumer, quarantine,
recovery dan pemasangan binary tetap pekerjaan berikutnya. Lihat
[SPEC M05](../specs/m05-durable-preparation.md).

Decoder envelope ketat dan storage quarantine source-aware juga lulus unit serta
PostgreSQL lokal: redelivery identik, konflik bytes pada offset sama, topic
incarnation berbeda, seal/inactive source, kegagalan commit/insert dan batas
ukuran. Koordinator consumer serial tersedia sebagai library dan lulus unit/race:
tidak fetch pesan berikut sebelum ack, tidak ack saat evidence gagal, retry
bounded, sanitasi panic, shutdown dan assignment-loss. Adapter Kafka nyata yang
memverifikasi source/generation serta recovery/wiring belum selesai; tidak ada
source produksi yang diaktifkan.

Recovery QUEUED dari database juga lulus lokal tanpa outbox/event Kafka: bounded
scan memakai Preparer yang sama, menyelesaikan snapshot/expiry, menghormati due
hold serta seal/epoch, dan mempertahankan deadline. Recovery PROCESSING/lease,
adapter Kafka nyata, requeue operator dan wiring proses belum selesai.

## 5. Checklist pelaksanaan

`[x]` berarti selesai dan ada bukti; `[ ]` berarti belum selesai. Sebagian
pekerjaan pada baris yang belum dicentang sudah dimulai, sesuai catatan.

### Tahap 1 — konteks dan target

- [ ] 01. Review/adopsi perubahan lama dan simpan dalam commit terpisah. Inventaris dan commit preservasi selesai; audit/adopsi terhadap rancangan reliability baru belum.
- [x] 02. Buat indeks status ADR, SPEC, backlog, dan dependensi awal (dokumen ini).
- [x] 03. Kunci scope inti v1; fitur tambahan yang disebut pengguna ditunda. Localisation bukan janji v1; batas input harus eksplisit dalam SPEC.
- [x] 04. Catat kebutuhan skala pengguna: sekitar 20.000 perangkat, 5 campaign/bulan, full broadcast. Sizing akhir menunggu benchmark.
- [ ] 05. Tetapkan SLO, RPO/RTO, dan acceptance threshold. Benchmark 60 detik baru usulan.
- [ ] 06. Verifikasi integrasi mobile/Laravel/token. Android+iOS dan transport dikonfirmasi, inspeksi mobile belum.

### Tahap 2 — rancangan

- [x] 07. Review dan revisi ADR-01/02 pada tingkat rancangan. D1-D7 dipetakan ke keputusan dan kasus verifikasi; penerimaan lintas ADR/runtime belum.
- [x] 08. Review/revisi ADR-03 dan transaksi idempotency + outbox pada tingkat rancangan; masih proposed, belum diimplementasikan.
- [x] 09. Review/revisi ADR-04 terhadap FCM untuk Android+iOS, credential, SDK dan lifecycle token pada tingkat rancangan; integrasi mobile/security belum divalidasi.
- [x] 10. Review/revisi ADR-05 terhadap device receipt, snapshot statistik dan versioned event ordering pada tingkat rancangan; belum diuji/diterima.
- [x] 11. Tulis ADR-06 cutover dan checklist evidence: enrollment React Native, exclusive cohort, coverage, pilot, rollback per stage dan retirement; masih proposed, belum dieksekusi.
- [x] 12. Tulis ADR-07 self-hosted Kafka topology/provisioning dan checklist evidence; masih proposed, tanpa perubahan infrastruktur/runtime.
- [x] 13. Tulis ADR-08 observability, katalog sinyal/alert dan checklist evidence; masih proposed, belum ada instrumentation/monitoring yang dipasang atau diuji.
- [x] 14. Tulis keputusan minimum ADR-09/10/11 dan readiness register API/data/security; masih proposed, policy/identity/RPO/RTO belum disetujui, tanpa perubahan runtime.
- [ ] 15. Review lintas ADR: pemeriksaan dan revisi teknis CR1–CR9 selesai pada tingkat rancangan; acceptance tetap conditional pada G2–G7. Tidak ada ADR yang otomatis accepted.

### Tahap 3 — fondasi implementasi

- [x] 16. Susun M00–M10 dengan dependency graph, output, gate dan kriteria selesai; implementasi milestone belum otomatis selesai.
- [x] 17. Regenerate mock segments; selaraskan actor/password/not-found fixtures staff dan filtered/batch count assertions; build dan seluruh unit test lulus pada 2026-11-02. Tidak ada perubahan perilaku service atau bukti integration/load test.
- [x] 18. GitHub Actions + script lokal format/modules/build/test/vet/vulnerability; local checks/actionlint lulus, security dependencies diperbarui dan reachable scan nol. Main workflow tetap; hosted run/ruleset belum diverifikasi karena belum di-push.
- [x] 19. Runner fixture terisolasi: 15 migration up/down/up, repository, kedua Kafka topic/producer round trip dan Firebase SDK HTTP simulation lulus lokal; guard/cleanup diverifikasi. Bukan test adapter/worker/new pipeline atau real FCM/APNs; hosted integration job belum tersedia.
- [ ] 20. Berjalan per milestone: SPEC M02a dan implementasi/18-case evidence selesai secara scoped; 56 role/route API cases plus negative DB/Kafka tests lulus lokal. M02b staff administration juga selesai scoped dengan 28 role/route API cases dan lifecycle/demotion tests lokal; M02c audit redaction dan SQL cleanup fixture lulus lokal. M02d membatasi 16 input JSON; boundary/48 rejection route cases lulus lokal. M02e memperketat parser JWT staff dan evaluasi baseline menetapkan minimum development guard siap untuk M03 terisolasi. Historical cleanup produksi dan release controls belum selesai; bukan bukti seluruh security/produksi selesai. M03a.1–3 registry/grants juga lulus lokal; M03b.1–M03b.5 selesai di repo dan lulus lokal. Cutover tujuan masih terbuka. M03c memiliki schema, proteksi, transaksi lifecycle, verifier, origin dan HTTP. M03d memiliki kontrak serta dua consumer sintetis lintas project yang lulus lokal. Pemulihan state dan invalidasi binding asli lulus lokal. Hook hasil provider M06, verifikasi consumer nyata dan kebijakan deployment tetap terbuka. Step 20 juga mencakup fondasi durable acceptance M04.

### Tahap 4 — delivery pipeline

- [ ] 21. Provider selection/credential, error contract, chunking dan invalid-token handling.
- [ ] 22. Atomic fan-out intent, device snapshot/identity dan unique constraints M05 lulus lokal, termasuk 20k intent; exclusive provider claim M06 belum.
- [ ] 23. Bounded retry/backoff, lease expiry, recovery, TTL/budget terminalization.
- [ ] 24. Decoder, source-aware dead-letter dan koordinator serial M05 lulus lokal; adapter Kafka/rebalance nyata dan operator reprocess belum.
- [ ] 25. Atomic send idempotency + claim + outbox, promotion/expiry dan bounded dispatcher M04 lulus lokal; scheduler binary belum memakai durable handoff.
- [ ] 26. Delivery update outbox dan consumer dedup/ordering contract.
- [ ] 27. Bounded concurrency, page/chunk memory limits, provider rate limit dan backpressure.
- [ ] 28. Acceptance/audit rollback, ack-before-commit, preparation/quarantine commit failure dan coordinator unsafe-offset lulus lokal; provider/adapter Kafka nyata dan failure windows lain belum.

### Tahap 5 — kontrak dan keamanan

- [ ] 29. Receipt dan statistik dengan arti status yang eksplisit.
- [ ] 30. Mobile token refresh, login/logout/account switch, multi-device dan migration tests.
- [ ] 31. Tutup correctness findings dalam scope; jangan anggap checklist code-review lama akurat tanpa verifikasi.
- [ ] 32. Authorization/ownership, JWT claim validation, rate/body limits, trusted proxy dan secrets.
- [ ] 33. Kontrak API/event dan contoh/error/compatibility tests untuk frontend serta mobile.

### Tahap 6 — operasi

- [ ] 34. Production images, resource sizing, config validation, deployment/migration procedure.
- [ ] 35. Graceful shutdown/drain dan connection lifecycle seluruh proses.
- [ ] 36. Health/readiness, logs, metrics, dashboard dan alert yang diuji.
- [ ] 37. Retention/purge untuk delivery/audit/key/outbox/dead-letter dan PII deletion.
- [ ] 38. Backup/restore serta runbook outage/requeue/credential rotation.
- [ ] 39. Owner layanan, alert escalation dan akses operator.

### Tahap 7 — bukti produksi

- [ ] 40. Staging end-to-end dengan perangkat Android/iOS nyata dan kondisi aplikasi relevan.
- [ ] 41. Load/burst/soak: 20.000 perangkat dan skenario headroom yang disepakati; catat environment, kuota dan hasil.
- [ ] 42. Failure injection: restart/rebalance, DB/Kafka outage, provider timeout/429/5xx, duplicate/replay.
- [ ] 43. Restore, deploy, rollback dan migration compatibility drill.
- [ ] 44. Security/operational readiness review; seluruh release blocker ditutup.
- [ ] 45. Pilot bertahap dengan traffic/perangkat uji yang terkontrol dan metrik keberhasilan.
- [ ] 46. Go/no-go berdasarkan bukti, cutover bertahap, dan pengamatan setelah rilis.

## 6. Release gates

Produksi hanya dinyatakan siap jika scope dan target numeriknya sudah ditetapkan,
seluruh acceptance tests relevan lulus, kapasitas terukur memenuhi target dengan
headroom, failure recovery tidak meninggalkan pekerjaan tanpa jalur pemulihan,
security blocker tertutup, restore/rollback terbukti, dan operator siap.

Receipt aplikasi mengukur laporan aplikasi, bukan bukti semua perangkat pasti
menerima push. Tidak ada klaim exactly-once end-to-end atau jaminan tiba di
perangkat dalam batas waktu tertentu. Catat duplicate window dan mitigasinya.

## 7. Sumber primer untuk keputusan provider

- [Firebase Apple setup](https://firebase.google.com/docs/cloud-messaging/ios/get-started): integrasi iOS dan konfigurasi APNs.
- [Firebase scaling guidance](https://firebase.google.com/docs/cloud-messaging/scale-fcm): retry/backoff, traffic shaping dan rollout bertahap.
- [Firebase token management](https://firebase.google.com/docs/cloud-messaging/manage-tokens): lifecycle dan invalid-token handling.

Sumber diperiksa pada 2026-11-02. Tidak ada credential atau isi `.env` yang
dibaca. Integrasi vendor dan mobile belum diuji; dokumen ini bukan hasil benchmark.
