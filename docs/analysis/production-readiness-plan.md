# Backend production readiness plan

Tanggal: 2026-11-02 (Asia/Jakarta). Status: perancangan aktif, belum siap produksi.

Dokumen ini adalah indeks pekerjaan dan kebutuhan terbaru. ADR tetap berstatus
`proposed` sampai keputusan dan review selesai. Checklist implementasi tidak
boleh dianggap selesai hanya karena rancangan atau file kode tersedia.

## 1. Kebutuhan yang sudah dikonfirmasi

| ID | Kebutuhan | Status / konsekuensi |
| --- | --- | --- |
| R1 | Repository ini hanya backend; portal admin di repository lain | Dikonfirmasi pengguna. Kontrak API termasuk scope, implementasi UI tidak. |
| R2 | Sekitar 20.000 perangkat aktif | Dikonfirmasi pengguna; jumlah user unik dan perangkat per user belum diketahui. |
| R3 | Sekitar 5 campaign per bulan | Dikonfirmasi pengguna; bukan 100 campaign/detik. |
| R4 | Broadcast terbesar menjangkau seluruh user aktif | Dikonfirmasi pengguna; fan-out harus menghitung seluruh perangkat eligible pada audience tersebut. |
| R5 | OneSignal akan diganti dengan FCM | Dikonfirmasi pengguna; fallback OneSignal permanen tidak menjadi kebutuhan. |
| R6 | Android dan iOS didukung | Dikonfirmasi pengguna. |
| R7 | Backend mengirim melalui FCM untuk kedua platform; iOS melalui APNs di sisi Firebase | Dikonfirmasi pengguna. Provider APNs langsung di backend tidak dibutuhkan untuk versi pertama. |
| R8 | Broadcast diinginkan sangat cepat | Dikonfirmasi sebagai arah kebutuhan; belum ada batas waktu numerik yang disepakati. |
| R9 | Aplikasi mobile memakai React Native | Dikonfirmasi pengguna; versi framework/native/library belum diverifikasi. |
| R10 | Kafka akan di-deploy di server perusahaan sendiri (self-hosted) | Dikonfirmasi pengguna; jumlah host/failure domain/resource dan operator belum diketahui. |
| R11 | V1 fokus broadcast/segment/individual, scheduling, recovery, receipt/statistik, keamanan dan operasi | Dikonfirmasi pengguna; UPLOAD, cancel/resubmit, rich media serta preferences/quiet hours ditunda. |

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
bukan bukti seluruh pipeline atau clean HEAD lulus. Tidak ada test integrasi
PostgreSQL/Kafka/provider nyata yang telah dijalankan di sesi perancangan ini.

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
| 11 | [Security baseline](../adr/adr-11-security-baseline.md) | Proposed: trust zones, JWT/session/refresh, deny-by-default permission, abuse/key/enrollment controls; belum diterapkan/diuji. |
| 12 | NEW: optional product parity | Kondisional; bukan penghalang produksi jika fitur tersebut tidak masuk scope. |

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
C21 adalah temuan permission runtime yang masih open. Retention/RPO/RTO,
identity policy dan perubahan kontrak masih perlu keputusan/verifikasi.

Hasil review lintas ADR: [CR1–CR9 dan gate G1–G7](adr-01-11-consistency-review.md).
Koreksi teknis sudah ditulis sebagai proposed; scope inti dikonfirmasi, tetapi
SLO/TTL, integrasi, host, policy keamanan/data dan bukti cutover masih terbuka.
Urutan dependency/milestone tersedia di [M00–M10](implementation-milestones.md).
Step 16 selesai sebagai pemetaan; berikutnya baseline test (step 17/M00).
Perbaikan baseline tidak harus menunggu seluruh gate release; acceptance SPEC
fitur sensitif menunggu keputusan yang dipakainya. Step 15 belum ditandai penuh
karena penerimaan ADR belum settled, bukan karena review dokumen belum dikerjakan.

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
- [ ] 17. Perbaiki test staff/segments, regenerate mock dan perbarui assertions; build/test hijau.
- [ ] 18. CI format/build/test/vet/vulnerability checks; tetap mengikuti workflow main.
- [ ] 19. Lingkungan PostgreSQL/Kafka/provider simulasi yang reproducible.
- [ ] 20. SPEC per milestone dengan acceptance cases; jangan tulis ulang semua detail terlalu awal.

### Tahap 4 — delivery pipeline

- [ ] 21. Provider selection/credential, error contract, chunking dan invalid-token handling.
- [ ] 22. Atomic fan-out intent, device snapshot/identity, unique constraints dan exclusive claim.
- [ ] 23. Bounded retry/backoff, lease expiry, recovery, TTL/budget terminalization.
- [ ] 24. Kafka processing/rebalance/commit safety, durable dead-letter dan operator reprocess.
- [ ] 25. Atomic send idempotency + claim + outbox; scheduler memakai durable handoff.
- [ ] 26. Delivery update outbox dan consumer dedup/ordering contract.
- [ ] 27. Bounded concurrency, page/chunk memory limits, provider rate limit dan backpressure.
- [ ] 28. Failure-window tests untuk seluruh batas database/provider/Kafka/offset.

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
