# Review dan revisi ADR-04/05

Tanggal: 2026-11-02 (Asia/Jakarta). Status: revisi rancangan selesai, masih
proposed; belum diuji atau diterima untuk implementasi/produksi.

Dokumen: [ADR-04](../adr/adr-04-provider-selection.md),
[ADR-05](../adr/adr-05-receipt-ingestion.md), dan kontrak terkait pada
[ADR-01](../adr/adr-01-provider-retry.md). Kutipan/temuan di bawah merujuk
versi sebelum revisi; tabel menjelaskan penggantinya. Kode runtime tidak diubah.

## Ringkasan temuan dan respons desain

| ID / prioritas | Temuan sebelum revisi dan dampak | Respons rancangan | Verifikasi yang diperlukan |
| --- | --- | --- | --- |
| P1 / blocker | ADR-04 default OneSignal walau pengguna meminta FCM; platform IOS dipetakan menjadi raw APNS | A4-1/A4-3: explicit FCM, typed address/project/app; IOS FCM tidak menjadi APNS; legacy belum eligible | Startup matrix, typed enrollment dan cohort cutover |
| P2 / blocker | Existing registerDeviceToken mempertahankan user lama; lifecycle refresh/transfer tidak melindungi intent | A4-3/A4-4: immutable binding per generation, installation proof/version, owner_epoch dan conditional invalidation | Concurrent refresh/logout/account switch, lost secret, old provider outcome |
| P3 / blocker | Cascade FK dapat menghapus pending delivery/history; unique raw token tidak mendukung riwayat immutable | A4-4: tombstone/restrictive deletion, active scoped uniqueness, controlled retention | Migration/backfill/PII lifecycle tests; ADR-10/11 masih diperlukan |
| P4 / blocker | Satu receipt user menandai semua device miliknya; body JWT/guessed notification tidak mengikat bukti perangkat | A5-1/A5-2: bearer user + signed per-delivery payload capability, original owner dan owner_epoch | Cross-device/foreign-user/forged capability/reassigned owner tests |
| P5 / blocker | PENDING receipt diberi 200 no-op tanpa menyimpan, sementara callback bisa datang sebelum hasil worker | A5-3 dan revisi A1-10: simpan bukti atomik; early receipt boleh memajukan PENDING; worker tidak boleh regress; late FAILED evidence terpisah | Both transaction orderings, retry suppression, failure-after-observation |
| P6 / blocker | DELIVERED ikut membuat user read; targeted memakai COALESCE(COUNT(...), live users) yang tidak pernah fallback dan tidak historis | A5-3/A5-4: read hanya OPENED; snapshot users/devices dan unit metric eksplisit | Multi-device stats, empty/incomplete/legacy snapshot, population changes |
| P7 / blocker | Unique(notification,user,status) menyatukan device; event tanpa stable ID/version dan dedup hilang saat outbox dihapus | A5-5: delivery identity/version, stable event ID, durable observation dedup, full projection event | Duplicate after drain, ack-before-delete, downstream version handling |
| P8 / blocker | ORDER BY + SKIP LOCKED batch tidak mencegah replica mengirim versi lebih tinggi lebih dahulu | A5-5: one-row bounded publish, anti-predecessor predicate per delivery, persisted retry, consumer version guard | Two drainers/locked predecessor/backoff/fairness tests |
| P9 / should-fix | SDK construction diklaim validasi credential lengkap; existing FCM adapter disebut production-quality tanpa execution; SendAll deprecated | A4-2/A4-5: bounded init vs remote permission evidence, ADC/file modes, supported bounded submission method | IAM/APNs preflight, SDK retry/concurrency audit, actual-device test |
| P10 / should-fix | Dokumen lama menyatakan FCM tidak punya delivery telemetry sama sekali | A5-1: client observations dipilih untuk v1; reporting/export opsional diakui dengan batas coverage/latency | Mobile reporting coverage; optional export dievaluasi jika dibutuhkan |

## Bukti dari repository dan dependency

- `internal/features/mobile/service.go`, `registerDeviceToken`: IOS menjadi
  APNS; update memakai UpdateDeviceTokenFull tanpa mengganti user_id.
- `internal/features/mobile/routes.go` dan `cmd/server/main.go`: /mobile/sync
  menggunakan body JWT; module belum memasang bearer auth untuk receipt.
- `internal/middleware/mobile_jwt.go`, `MobileJWTMiddleware.Use`: bearer HS256
  dan external_id sudah tersedia untuk device-token routes; expected issuer/
  audience dan required expiration belum ditegakkan. Auth server/mobile issuer
  harus disepakati, bukan disimpulkan sudah aman dari nama middleware.
- `db/migrations/000002_create_device_tokens.up.sql`: unique push_token global
  dan user FK cascade. `000012_create_notification_deliveries.up.sql` memiliki
  cascade user/device. `000013_create_notification_reads.up.sql` memiliki
  unique(notification_id,user_id), cocok untuk user projection, bukan device fact.
- `internal/kafka/events.go`, DeliveryUpdated: notification/user/provider/status
  tanpa event ID, delivery ID dan version. ProviderMessageID bukan pengganti
  identitas delivery yang stabil di seluruh attempt/provider.
- `go.mod`: Firebase Admin Go v4.21.0. Source dependency
  `messaging/messaging_batch.go`: SendAll deprecated, SendEach <=500 dan
  internal numWorkers=50. `messaging/messaging.go`: Message.Fid tersedia,
  Message.Token deprecated. Jangan mengasumsikan mobile SDK yang belum dibaca
  mendukung identifier modern hanya karena server SDK mendukungnya.

## Keputusan tambahan dan batas scope

1. Proposed FCM path tidak memerlukan OneSignal credential atau backend APNs
   client. Firebase project mengelola APNs integration; akses operator/project
   owner masih perlu ditentukan. Tidak ada auto-fallback lintas provider.
2. Modern registration memakai existing /device-tokens bearer surface dengan
   extension eksplisit; /mobile/sync lama tetap legacy selama migrasi. Semua
   entry point harus memakai service invariants yang sama agar tidak ada bypass.
3. owner_epoch berubah pada logout/account switch, terpisah dari address version.
   Refresh alamat pada user yang sama tetap dapat melaporkan push sebelumnya;
   pindah akun lalu kembali tidak menghidupkan receipt epoch lama.
4. Signed receipt capability membuktikan payload possession dalam trust model
   client/user; bukan hardware attestation. Lifetime 7 hari, key rotation dan
   enrollment proof masih proposal untuk review keamanan/mobile.
5. OPENED/DELIVERED boleh mendahului confirmed provider acceptance. Simpan
   fakta terpisah; jangan mengarang provider_message_id/sent_at. FAILED dengan
   observasi terlambat tetap terminal untuk retry dan ditampilkan sebagai
   discrepancy, bukan otomatis dihidupkan kembali.
6. Event schema v2 dan stats baru perlu compatibility review dengan consumer
   dan frontend repo. Ini membatalkan klaim lama bahwa wire payload tidak berubah.

## Pertanyaan integrasi dan release gates

| Keputusan | Pemilik/fakta yang diperlukan | Batas progres |
| --- | --- | --- |
| Firebase project/app/environment | Pemilik Firebase/ops; Android package/iOS bundle dan APNs environment | Sebelum credential config dan pilot |
| Mobile framework dan versi SDK | Repo/tim mobile; FID enrollment versus token compatibility | Sebelum kontrak registrasi/SPEC diterima |
| Laravel issuer/audience/expiry/logout | Pemilik auth dan mobile | Sebelum auth/receipt endpoint dipublikasikan |
| Installation credential/recovery dan app attestation | Mobile + security; storage/rotation/ownership proof | Sebelum enrollment produksi |
| Retention, PII deletion, receipt window | Produk/ops/security, ADR-10/11 | Sebelum schema/SPEC diterima |
| DeliveryUpdated consumers | Inventory analytics/integrasi di luar repo | Sebelum enable event v2 |
| Final SLO dan rate budget | Benchmark/quota/ops | Sebelum tuning dan go/no-go |

Tidak meminta raw secret/private key. Pertanyaan ini tidak menghalangi penulisan
ADR-06–11, tetapi tidak boleh diganti asumsi tersembunyi ketika mulai implementasi.

## Metode dan verifikasi

Review dilakukan dengan membaca source, SQL, middleware, ADR dan dependency
lokal. Orbit discovery dicoba tetapi gagal DNS resolution; fallback memakai
dokumentasi primer Firebase dan pinned SDK. Tidak ada request FCM, init live
credential, read `.env`, uji perangkat, Postman workspace push, migration,
test runtime atau benchmark dalam pekerjaan dokumentasi ini.

Sumber primer diperiksa 2026-11-02:
[Admin setup](https://firebase.google.com/docs/admin/setup),
[Apple setup](https://firebase.google.com/docs/cloud-messaging/ios/get-started),
[send methods](https://firebase.google.com/docs/cloud-messaging/send/admin-sdk),
[registration management](https://firebase.google.com/docs/cloud-messaging/manage-tokens),
[delivery telemetry](https://firebase.google.com/docs/cloud-messaging/understand-delivery).

Review ini menyelesaikan step 9–10 pada tingkat rancangan. Implementasi dan
verifikasi masih terbuka. Berikutnya step 11: ADR-06 migrasi OneSignal/mobile,
ditulis berdasarkan kontrak typed enrollment dan receipt yang direvisi, tanpa
menjanjikan konversi identifier atau zero-impact rollback yang belum dibuktikan.
