# OneSignal → FCM cutover readiness

Tanggal: 2026-11-02. Status: checklist rancangan, **belum izin cutover**.
Rancangan: [ADR-06](../adr/adr-06-onesignal-fcm-cutover.md).

React Native, Android+iOS, OneSignal saat ini dan tujuan FCM telah dikonfirmasi.
Project Firebase kemungkinan sudah ada menurut pengguna, tetapi identity,
akses, app/environment dan readiness belum diverifikasi. Checklist kosong
berarti belum ada bukti, bukan otomatis ada masalah pada sistem pengguna.

## Evidence register

| Gate | Bukti yang harus dicatat | Pemilik peran | Status |
| --- | --- | --- | --- |
| C01 Firebase inventory | Project/app/package/bundle/environment IDs, IAM owner, quota dan APNs setup; lokasi bukti tanpa secret | Mobile + ops | Belum diverifikasi |
| C02 React Native compatibility | RN/package/native versions, address type didukung dan enrollment callbacks | Mobile | Framework dikonfirmasi; versi belum |
| C03 Legacy behavior | Apakah ada pengiriman produksi/scheduled automation, operator dan cohort exclusion/pause | Produk + ops | Belum diverifikasi |
| C04 Registry/consent census | User/device/installation counts, owner/epoch, unknown legacy, permissions/opt-out dan aktivitas | Backend + mobile | Skala perkiraan tersedia; census belum |
| C05 Modern enrollment | Auth, binding proof/version, refresh/logout/transfer, typed identity dan recovery | Backend + mobile + security | Proposed ADR-04; belum diimplementasikan |
| C06 Delivery reliability | Atomic intent/outbox, budget/TTL, lease, safe offset, fault/replay evidence | Backend | Proposed ADR-01–03; belum diuji |
| C07 Receipt/events | Device capability, early/late receipt, stats units, event v2 and consumer compatibility | Backend + mobile + consumer owner | Proposed ADR-05; belum diuji |
| C08 Isolation | Tidak ada business campaign sama ke cohort yang tumpang tindih lintas provider | Produk + ops | Belum dibuktikan |
| C09 Real devices | Android/iOS release builds: permissions, foreground/background/terminated behavior, tap/deep-link, offline and upgrade | Mobile + QA | Belum diuji |
| C10 Capacity/observability | Agreed numeric SLO, rate/quota, load/soak, dashboards, tested alerts and unknown-outcome accounting | Backend + ops | Belum ditetapkan/diuji |
| C11 Security/retention | Auth/ownership, key/installation secret lifecycle, logs, limits, deletion and backup/restore | Security + ops | ADR-10/11 belum ditulis |
| C12 Stage rollback | Hold/drain/reconcile/compatible-backend rollback, client capability and restore drill | Backend + mobile + ops | Belum diuji |
| C13 Ramp decision | Cohort, count, dwell time, stop threshold, sign-off record and observation window | Produk + mobile + ops | Belum ditetapkan |
| C14 Full coverage | Full eligible-audience gate or explicit accepted re-scope/straggler policy | Produk | Belum diputuskan |
| C15 Retirement | Legacy jobs disabled, SDK-free release verified, dependencies audited and credential/data cleanup sequenced | Mobile + ops | Belum dijalankan |

Actual nama pemilik, tanggal, artifact/report link dan hasil ditambahkan ketika
tersedia. Secret/key/file credential tidak boleh menjadi isi register.

## Urutan eksekusi setelah implementasi dan staging siap

- [ ] Inventory Firebase/RN/legacy dan tetapkan target/stop thresholds.
- [ ] Implementasikan dan verifikasi ADR-01–05 serta kontrol eligibility/hold.
- [ ] Verifikasi schema consumer/read API sebelum event v2 diaktifkan.
- [ ] Rilis/test modern enrollment tanpa mengaktifkan pengiriman FCM produksi.
- [ ] Buktikan coexistence atau pilih transisi maintenance terkontrol.
- [ ] Jalankan test-device campaign dengan izin penerima dan cohort yang jelas.
- [ ] Latih hold/recovery/rollback dengan hasil provider yang belum pasti.
- [ ] Aktifkan pilot FCM-only dan cek isolasi terhadap sender legacy.
- [ ] Evaluasi report berdasarkan denominator, app version, rate dan coverage.
- [ ] Naikkan cohort bertahap setelah threshold dan dwell time terpenuhi.
- [ ] Buka full broadcast hanya setelah coverage/re-scope disepakati.
- [ ] Observasi sesuai window, lalu retire OneSignal dengan dependency audit.

Tidak ada live send atau perubahan account/provider pada sesi penulisan ini.
Backend send gate tidak dapat membatalkan push yang sudah diterima provider.
Rollback aplikasi yang sudah menghapus SDK lama membutuhkan jalur FCM yang
kompatibel atau rilis mobile baru; bukan sekadar mengganti environment variable.
