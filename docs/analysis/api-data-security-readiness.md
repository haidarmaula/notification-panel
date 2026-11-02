# API, data lifecycle and security readiness

Tanggal: 2026-11-02. Status: proposed design gates, belum implementasi/policy
approval/security test. Dokumen:
[ADR-09 API](../adr/adr-09-api-consistency.md),
[ADR-10 retention/recovery](../adr/adr-10-retention-recovery.md),
[ADR-11 security](../adr/adr-11-security-baseline.md).

## Minimum architectural decisions recorded

- Pertahankan envelope API; tambahkan safe machine error codes, presence-aware
  PATCH, conditional writes, filter/count consistency dan compatibility map.
- Critical mutation/audit commit bersama; jangan simpan secret dalam audit JSON.
- Pisahkan deactivation/tombstone, PII erasure, archive dan backup retention.
- Pending work/outbox/lease tidak dihapus hanya karena umurnya melewati cutoff.
- Backup/PITR harus dapat dipulihkan secara terisolasi; restore mode tidak
  dapat mengirim push hanya karena dispatch flag dari backup bernilai true.
- Source epoch/checkpoint dan reconciliation melindungi consumer/identity
  setelah DB restore; tidak menjanjikan nol duplicate/loss eksternal.
- Browser tidak menyimpan shared server API key; user/session identity dan
  operation/object permission berlaku terpisah dari network/API-key trust.
- Staff refresh/session revocation, mobile issuer/audience/expiry, key separation,
  binding proof, proxy/origin/resource/rate policy adalah bagian baseline.

## Verified code evidence and limits

| Evidence | Meaning / caveat |
| --- | --- |
| pkg/response/response.go | success/message/data tersedia; Encode error setelah header belum ditangani |
| staff/profile dto.go dan segments/helpers.go | Field presence serta invalid pagination belum konsisten |
| notifications/model.go vs SQL constraints | SENDING/notification-SENT constants tidak cocok vocabulary runtime |
| notifications/routes.go + server wiring + NotificationService.Create/Send | Mutation dibungkus API key/JWT/audit; tidak ada role permission check yang membatasi VIEWER |
| segments/routes.go dan service/member methods | Read-only role belum dibedakan pada jalur mutasi; temuan C21 dicatat open |
| 000015_seed_roles.up.sql | VIEWER memang dideskripsikan Read only; bukan kebijakan yang sudah ditegakkan |
| token/jwt.go dan auth/service.go | Existing staff JWT/refresh belum mempunyai persistent rotation/revocation family |
| middleware/mobile_jwt.go | HS256/external_id tersedia; required exp/issuer/audience perlu diperketat |
| middleware/audit.go | Trusted-peer/XFF guard sudah ada; default private ranges terlalu luas untuk dianggap exact production proxy inventory |
| Existing FK/compose/Makefile | Cascade dan volume dev terlihat; tidak membuktikan ada/tidak ada backup perusahaan di luar repo |

Review ini source-only. Tidak ada akun VIEWER yang digunakan untuk melakukan
mutasi, credential yang dibaca, role/password yang diubah, restore/purge, atau
live API/security/provider test. Rancangan tidak menutup C21 maupun finding lama.

## Outstanding requirement / evidence gates

| Gate | Decision or evidence needed | Before |
| --- | --- | --- |
| A01 contract ownership | Existing caller inventory, OpenAPI/API version map, PATCH/status/auth compatibility | SPEC accepted |
| A02 transactional correctness | Safe error/serialization, list/filter snapshot, audit rollback and CAS tests | Implementation accepted |
| A03 role matrix | Business role matrix dikonfirmasi pengguna 2026-11-02; scoped SPEC M02a tersedia. Schedule durability/delegation implementation dan operational step-up masih pending | M02a role enforcement dapat dimulai; remaining authorization/security gates belum ditutup |
| D01 retention matrix | Owner/purpose/lifetime/action for each class; explicit pending-work exclusions | Retention/erasure SPEC accepted |
| D02 RPO/RTO | Allowed lost-data interval and safe-service restoration time, DB/storage failure domains | Backup/restore design accepted |
| D03 recovery proof | Base/WAL/key continuity, sealed restore, erasure/revocation replay, epoch/ID/consumer/provider reconciliation | Production go/no-go |
| D04 archive/history | Coverage semantics, late-report/deletion behavior, checkpoint retention and consumer tombstones | Archive/purge enabled |
| S01 identity perimeter | Gateway/origins/exact trusted proxies, browser/BFF route auth, Laravel claims | Auth SPEC accepted |
| S02 session/privilege | Refresh rotation/reuse policy, logout/offboarding, step-up/MFA/company IdP or controlled operator channel | Production privileged access |
| S03 secret/data custody | Key distribution/encryption/rotation/restore, installation secure storage and receipt window | Production sensitive data |
| S04 enrollment proof | React Native support, app proof versus address-possession recovery/challenge, conflict policy | Modern enrollment enabled |
| S05 bounded abuse | Body/target/hash/request/concurrency budgets, aggregate gateway/local limits and failure mode | Public routes enabled |
| S06 verification | Role/object/claim/refresh/limiter/secret-negative tests, dependency/image/secret checks, restore revocation drill | Production go/no-go |

Tidak ada pilihan perusahaan yang diisi dengan asumsi disetujui. RPO/RTO dan
retention berbeda dari Kafka replication; backup juga tidak membatalkan push
yang sudah diterima FCM. Policy sign-off tidak digantikan oleh scanner hijau.

## Cross-ADR requirements reviewed at step 15

1. Origin business epoch terpisah dari source epoch pada kedua event envelope dan consumer checkpoint, stabil pada
   normal restart; recovery snapshot/epoch aktivasi tidak boleh otomatis resend.
2. Erasure/tombstone propagation dan minimal replay checkpoint perlu event/schema
   review; bukan cuma DELETE SQL pada tabel hot.
3. Shared service permission mencakup scheduled_at pada create/PATCH; audit kritis
   menjadi bagian transaksi, bukan best-effort sesudah commit.
4. Browser API-key/cookie/session rollout dapat memerlukan auth contract version;
   body-JWT sync lama tidak boleh menjadi enrollment bypass.
5. Revocation/ownership yang mundur setelah restore harus direkonsiliasi sebelum
   API/delivery kembali terbuka, terpisah dari sekadar postgres restore selesai.

Step 14 selesai sebagai dokumentasi minimum. Hasil step 15 tercatat dalam
[consistency review](adr-01-11-consistency-review.md): CR1–CR9 direvisi pada
tingkat rancangan, gate eksternal G2–G7 masih terbuka. Berikutnya step 16
menyusun dependency/milestone map. SPEC/implementasi dimulai hanya untuk bagian yang kontraknya sudah cukup
jelas dan diverifikasi; tidak perlu menganggap seluruh proposal diterima.
