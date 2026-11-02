# ADR-01–11 consistency review

Tanggal: 2026-11-02. Status: review rancangan; ADR tetap **proposed**.
Tidak ada perubahan runtime, migration, provisioning, atau bukti produksi.
Indeks kebutuhan: [production plan](production-readiness-plan.md).

## 1. Scope dan batas penerimaan

Pengguna mengonfirmasi v1 fokus broadcast/segment/individual, scheduling,
recovery, receipt/statistik, keamanan dan operasi. UPLOAD, cancel/resubmit,
rich media dan preferences/quiet hours ditunda. Emergency hold, idempotency,
token/logout lifecycle, pembatasan traffic dan audit adalah kontrol inti.
Localisation bukan janji fitur v1; kontrak harus menjelaskan input yang didukung.

Kafka self-hosted dikonfirmasi; jumlah host independen **belum diketahui**.
Repo React Native tersedia tetapi lokasinya belum diberikan. Repo Laravel
tidak tersedia; aplikasinya berjalan langsung di server. Verifikasi auth dapat
memakai kontrak/config teredaksi serta token uji melalui pemilik sistem;
tidak membutuhkan penyalinan credential produksi atau akses server otomatis.

Review ini memisahkan koreksi teknis yang dapat dirancang sekarang dari pilihan
produk/operasional yang memerlukan pemilik keputusan. Tidak semua gate release
harus selesai sebelum memperbaiki baseline test; penerimaan SPEC suatu milestone
memerlukan keputusan yang benar-benar dipakai milestone itu.

## 2. Temuan lintas ADR dan resolusi proposed

| ID | Konflik / risiko | Resolusi rancangan | ADR terkait |
| --- | --- | --- | --- |
| CR1 | Urutan lock idempotency, receipt, registration dan revocation belum satu protokol | Urutan bersama di bagian 3; uji race dan implicit FK locks dalam SPEC | 01,03,04,05,09,10,11 |
| CR2 | “Consistent snapshot” tidak menentukan isolation; coverage/count/intent bisa berbeda | Preparation REPEATABLE READ, satu predicate/snapshot, whole-transaction retry terbatas | 01,05,06 |
| CR3 | Reservasi satu page menghabiskan budget sebelum breaker/rate permit tersedia | Page kandidat tanpa charge; reserve satu perangkat setelah permit, attempt ID dan fencing | 01,04 |
| CR4 | Breaker menghitung batch, executor FCM menghitung perangkat | Satu outcome logical submission perangkat; retry internal SDK bukan outcome tambahan | 01,04,08 |
| CR5 | Deadline dari actual promotion menghidupkan campaign basi setelah outage | Scheduled deadline = scheduled_for + TTL; expired schedule FAILED tanpa outbox/fanout palsu | 01,03,06,08 |
| CR6 | Late observation pada FAILED bisa disalahartikan sukses saat agregasi campaign | FAILED tetap submission failure; observation axis terpisah meskipun campaign masih PROCESSING | 01,05,08 |
| CR7 | Topic name/partition/offset bisa dipakai ulang setelah topic recreation | Quarantine unik berdasarkan physical log source incarnation + partition + offset | 02,07,10 |
| CR8 | source_epoch mencampur identitas acceptance, recovery dan Kafka source | Pisahkan origin_epoch, source_epoch dan log_source_id; bagian 4 | 02,03,05,07,10 |
| CR9 | Expiry capability dari waktu schedule diterima bisa habis sebelum pengiriman | Receipt capability expiry = persisted deadline_at + proposed 7d | 01,05,10,11 |

Resolusi tersebut sudah dimasukkan ke ADR terkait atau protokol bersama di sini.
Status “resolved in design” tidak menutup finding runtime `code-review.md`.
Nilai timeout, TTL, breaker, rate/concurrency dan retention tetap proposed.

## 3. Protokol transaksi dan lock bersama

Jika operasi perlu mengambil beberapa kelompok row locks, urutannya:

1. Control/policy rows yang memang harus dikunci, lalu principal staff/user,
   kemudian session/revocation rows. Urutkan tabel dan ID secara stabil;
   actor/target staff memakai urutan ID yang sama, bukan actor selalu dahulu.
2. Idempotency key, kemudian notification/campaign rows, diurutkan ID.
3. Installation rows, immutable registration/binding rows, lalu delivery rows;
   masing-masing diurutkan ID. Attempt/result/finalization mengambil campaign
   sebelum delivery; receipt juga mengambil installation/binding sebelum delivery.
4. Evidence/read/projection, audit dan outbox writes di transaksi yang sama.

Operasi hanya mengunci row yang dibutuhkan; aturan ini bukan perintah mengunci
seluruh audience/principal atau satu global mutex untuk setiap submission.
Cryptographic verification dan network I/O berada di luar transaksi; current
authority/owner/version yang menentukan mutasi direcheck di transaksi.
Renewal campaign tidak mengambil lock installation lalu kembali ke campaign.

Registration/logout dapat memakai principal → installation → binding tanpa
campaign. Jika perlu maintenance campaign, commit revocation dulu lalu lakukan
fase campaign secara terpisah dengan recheck; jangan sambil menahan installation
baru mencari campaign. Discovery maintenance memakai ID tanpa child row lock,
kemudian membuka transaksi dengan urutan di atas dan mengecek ulang kondisi.
Read snapshot tidak memberikan kewenangan mengirim ke owner yang sudah berubah;
executor tetap memeriksa current registration sebelum setiap claim.

Outbox dispatcher mengunci satu outbox row dan tidak berbalik mengambil parent
campaign/principal. Bila operasi memerlukan audit FK/principal lock, ambil yang
diperlukan sebelum child locks. SPEC harus memeriksa implicit FK locks, unique
conflicts dan trigger, bukan hanya SELECT FOR UPDATE yang tertulis. Lock/statement
timeouts dan whole-transaction retry 40001/40P01 dibatasi; tidak ada klaim bebas
deadlock berdasarkan diagram saja. Network provider tidak berjalan sambil
menahan campaign lock; bounded Kafka outbox publication adalah pengecualian
yang eksplisit milik ADR-03/05, bukan izin network I/O di transaksi lain.

Preparation memakai REPEATABLE READ untuk coverage, audience count dan set-wise
intent insert pada snapshot yang sama. Snapshot bukan seluruh database yang
dibekukan selamanya: perubahan eligibility berikutnya dicek executor. Complete
marker/counts hanya terlihat setelah commit. Expiry sebelum preparation boleh
FAILED dengan snapshot unknown; hanya complete snapshot boleh menghasilkan
COMPLETED atau agregasi outcome perangkat. Empty snapshot bukan unknown.

Pemilihan isolation dan retry didasarkan pada dokumentasi primer PostgreSQL:
[transaction isolation](https://www.postgresql.org/docs/17/transaction-iso.html)
dan [explicit locking](https://www.postgresql.org/docs/17/explicit-locking.html).
Ini dasar rancangan, bukan hasil integration test pada database project.

## 4. Identitas, versi dan recovery

| Identitas | Arti dan invariant |
| --- | --- |
| origin_epoch + notification_id | Business acceptance yang immutable; existing intent tidak mendapat identitas bisnis baru saat restore |
| source_epoch | Lineage publikasi/projection yang diaktifkan lewat recovery manifest; stabil antar restart biasa |
| log_source_id + partition + offset | Lokasi fisik source Kafka; cluster dan topic incarnation wajib diketahui |
| delivery_id | Identitas opaque per-device intent; tidak berubah antar retry |
| lease_generation + attempt_id | Fence DB result terhadap lease takeover dan reservasi perangkat ganda |
| owner_epoch | Versi otorisasi owner installation, berubah logout/account switch |
| route_epoch | Versi policy/cohort cutover; bukan otorisasi mengubah audience lama |
| event_id / delivery_version | Dedup event global / monotonic projection version dalam source lineage yang disetujui |

Send dan update envelopes membawa origin_epoch serta source_epoch. Admission
mengecek lineage yang diizinkan sebelum mencocokkan identitas business row.
Requeue biasa mempertahankan kedua epoch asli, tidak menambahkan current epoch
agar lolos validasi. Manifest recovery secara eksplisit menentukan lineage mana
yang boleh direkonsiliasi; old payload/event ID tidak dilabel ulang.

Recovery snapshots memakai event ID dan source lineage baru yang disetujui,
mempertahankan origin business identity. Consumer mengaktifkan replacement
projection secara eksplisit, tidak menjumlahkan snapshot recovery sebagai
delivery baru. Consumer checkpoints memakai (source_epoch, delivery_id),
global event-ID dedup, dan monotonic version di lineage aktif. Epoch lama/asing
tidak boleh menghidupkan data yang sudah dihapus/direvoke. Consumer inventory
dan persetujuan schema rollout tetap terbuka sebelum event v2 diaktifkan.

Setiap executor hanya reserve perangkat setelah concurrency/rate/breaker permit.
Satu unresolved claim tidak dapat diambil dua executor pada lease generation
yang sama. Hasil cocok dengan attempt ID serta generation; crash sesudah commit
claim mempertahankan charge, bukan refund spekulatif. Lease takeover dan unknown
external outcome tetap dapat menyebabkan duplicate physical push: exactly-once
provider delivery tidak dijanjikan.

## 5. Keputusan luar repo dan milestone yang dibatasi

| Gate | Kondisi / pemilik yang perlu ditentukan | Memblokir |
| --- | --- | --- |
| G1 Scope | Core dikonfirmasi; input yang tidak didukung harus eksplisit | Batas contract SPEC, bukan baseline tests |
| G2 Freshness/performance | TTL, numeric SLO, acceptance target dan quota aggregate belum disepakati | Final scheduling/retry config, benchmark acceptance dan release |
| G3 Mobile/auth | Lokasi repo RN, SDK/native version, issuer/audience/JWKS/revocation contract Laravel belum diverifikasi | Enrollment/receipt auth SPEC acceptance dan cutover |
| G4 Infrastructure | Kafka host/failure domains, DB/staging/TLS dan operator belum diketahui | Production deployment/HA claim |
| G5 Security policy | Business role matrix dikonfirmasi pengguna; M02a scoped SPEC dan operation guards locally verified. Enrollment proof/recovery, gateway/BFF/operational policy tetap terbuka | Enabling modern enrollment/identity/perimeter dan remaining security SPEC; bukan penghalang M02a role enforcement. C21 remediated dalam scope M02a; remaining security gates tetap terbuka |
| G6 Data/recovery | Retention, erasure, RPO/RTO, backup ownership dan consumer inventory belum ditentukan | Maintenance/recovery SPEC acceptance dan release |
| G7 Firebase/cutover | Project/app/APNs config, credentials access, legacy automation/exclusive cohort belum diverifikasi | Real provider/device pilot dan OneSignal retirement |

Baseline test repair dan review staged processor dapat dimulai tanpa akses
Firebase/server Laravel atau angka SLO final. Implementasi sensitif mengikuti
SPEC milestone yang mencatat gate terkait; tidak menerima semua ADR secara
massal hanya untuk memulai coding. Step 15 tetap conditional sampai keputusan
yang diperlukan ditutup dan penerimaan ADR dicatat per dokumen.

## 6. Kasus verifikasi wajib dalam SPEC berikutnya

| Kasus | Bukti yang harus dihasilkan, belum dijalankan |
| --- | --- |
| Send/revocation/receipt/registration bersamaan | Bounded completion, tidak reverse-lock atau mengubah owner lain |
| Audience berubah antara count dan insert | Satu committed snapshot/count; aborted transaction tidak meninggalkan partial intents |
| Dua executor memilih perangkat yang sama | Satu committed attempt ID/charge pada lease generation itu |
| Breaker terbuka di tengah page | Kandidat tanpa permit belum charged; admitted in-flight work terukur |
| Scheduler down melewati deadline | FAILED/ttl_expired, tidak fresh TTL atau provider request |
| Expiry sebelum fanout | Unknown denominators, tidak COMPLETED/zero-device palsu |
| Receipt OPENED pada FAILED saat PROCESSING | Late observation/read tersimpan, campaign tidak mendapat submission success palsu |
| Topic dihapus/dibuat ulang dengan offset sama | Source incarnation berbeda; DLQ lama tidak menimpa yang baru |
| Restore dan old Kafka numeric ID sama | Origin/lineage mismatch ditahan; tidak mengirim ulang karena ID collision |
| Recovery snapshot setelah newer external projection | Explicit lineage activation/replacement; tidak double-count atau resurrect erased data |
| Campaign dijadwalkan jauh di depan | Capability berlaku untuk send window, tidak expired dari waktu draft/schedule creation |

Pemeriksaan dokumen meliputi link lokal, struktur ADR, whitespace dan konsistensi
teks. Semua kasus concurrency/failure di tabel ini membutuhkan integration test;
belum ada hasil runtime, load, mobile atau restore yang dibuktikan review ini.
