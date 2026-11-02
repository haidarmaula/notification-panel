# Review awal ADR-01 dan ADR-02

Tanggal: 2026-11-02. Status: review awal dan tindak lanjut revisi desain;
belum diterima untuk implementasi.
Scope: hubungan retry ledger, fan-out, ownership attempt, Kafka consumption,
offset, recovery, dan handoff API. Tidak ada perubahan runtime dalam review ini.

Dokumen yang direview: [ADR-01](../adr/adr-01-provider-retry.md),
[ADR-02](../adr/adr-02-worker-consumption.md), serta hubungan yang dinyatakan
oleh [ADR-03](../adr/adr-03-transactional-outbox.md). Review/revisi ADR-03
dan [review lanjutan ADR-04/05](adr-04-05-review.md) sudah tersedia pada
tingkat rancangan; penerimaan lintas ADR tetap step 15. Kebutuhan terbaru ada pada
[production plan](production-readiness-plan.md).

## Kesimpulan

Pemisahan provider retry yang persisten dari consumer retry adalah arah yang
baik. D1-D7 di bawah merekam temuan terhadap versi **sebelum revisi** pada
2026-11-02; kutipan lama bukan perilaku yang masih disarankan oleh ADR terbaru.
ADR-01/02/03 sudah direvisi untuk menanganinya pada tingkat rancangan. Status
proposed tetap berlaku; belum ada verifikasi runtime atau penerimaan produksi.

## Tindak lanjut revisi 2026-11-02

| Temuan | Keputusan rancangan terbaru | Bukti yang masih diperlukan |
| --- | --- | --- |
| D1 | ADR-02 A-1/A-3: satu admission in-flight, berhenti fetch saat DLQ/commit gagal; provider pool terpisah | Integration test offset, rebalance, commit failure dan shutdown |
| D2 | ADR-01 A-10: campaign lease dengan owner/generation/expiry; claim budget per delivery | Lease expiry, takeover dan stale-result tests; residual external duplicate tetap ada |
| D3 | ADR-01 A-6/A-11: scan expired lease/budget/TTL, receipt states, dan QUEUED sebelum admission | Clock/fault tests untuk final claim, queue expiry dan pre-admission quarantine |
| D4 | ADR-01 A-10: atomic audience snapshot + completion marker, bukan row-count guard | Transaction rollback/snapshot benchmark 20.000 perangkat dan kebijakan legacy/FK |
| D5 | ADR-01 A-8 + ADR-03 A-1: key, claim, deadline dan outbox satu transaksi | Concurrent-key, rollback, unknown-commit dan authorization tests |
| D6 | ADR-01 A-3/A-4/A-9 + ADR-02 A-1/A-5: device-based sizing, provider retry floors, operation deadlines | Quota/SDK audit, benchmark dan konfigurasi final |
| D7 | ADR-02 A-2/A-4: source-position dedup, replay sebagai event baru dan resolution audit | Replay-after-ack-crash dan concurrent-operator tests |

Perubahan tambahan: ADR-03 menghapus eager publisher server, memakai satu
row per transaksi publish yang dibatasi timeout, dan tidak menjanjikan poll
interval sebagai batas waktu mutlak. Budget retry bergeser dari campaign ke
perangkat agar crash di tengah broadcast tidak menghabiskan jatah perangkat
yang belum pernah diajukan. Setelah review lintas ADR, TTL immediate memakai
durable acceptance, sedangkan scheduled memakai scheduled_for; worker membaca
deadline yang sudah persisted. Lihat [CR1–CR9](adr-01-11-consistency-review.md)
untuk protokol terbaru; keputusan harus masuk SPEC sebelum implementasi terkait.

## D1 — Blocker: pesan tanpa dead-letter dapat terlewati commit berikutnya

**Bukti:** ADR-02 A-2 menyatakan dead-letter insert gagal -> jangan commit,
tetapi lanjut ke pesan berikutnya. A-3 menganggap commit per pesan mencegah
pesan sebelumnya ikut ter-commit. Pada dependency yang dipakai repo,
`github.com/segmentio/kafka-go@v0.4.51/reader.go`, Godoc `Reader.CommitMessages`
menyatakan commit offset tertinggi juga meng-commit seluruh offset sebelumnya
pada partition tersebut. Perilaku ini berlaku meski setiap call hanya berisi
satu pesan.

**Dampak:** error database dapat menghilangkan event dari jalur recovery Kafka
meski dead-letter belum tersimpan. Pool ADR-01 A-9 menambah risiko completion
tidak berurutan: offset lebih tinggi selesai lebih dahulu.

**Revisi yang diusulkan:** definisikan contiguous safe commit watermark per
partition. Offset hanya boleh maju melewati pesan yang selesai secara durable.
Jika ada gap, tahan commit melewati gap dan batasi intake/in-flight; pilih
strategi pause/stop/restart atau coordinator yang eksplisit. Tetapkan lifecycle
rebalance dan shutdown. Jangan mengandalkan commit per-call sebagai isolasi.

**Kasus wajib:** offset 10 gagal diproses dan gagal dead-letter, offset 11
sukses; setelah restart offset 10 tetap tersedia. Ulangi ketika pool
menyelesaikan 11 sebelum 10 dan ketika assignment partition berpindah.

## D2 — Blocker: atomic increment belum menjadi exclusive attempt claim

**Bukti:** ADR-01 A-10 mengusulkan UPDATE increment `attempt_count` dengan
predicate status PROCESSING, due time, budget, dan deadline. Tidak disebutkan
perubahan predicate yang menandai attempt sedang dimiliki worker. A-9 juga
menganggap deterministic Kafka key menjaga ordering saat memakai pool umum.

**Dampak:** dua driver dapat menjalankan UPDATE berturut-turut dan keduanya
berhasil ketika `next_attempt_at` tetap NULL/due dan budget belum habis.
Masing-masing mengirim PENDING set yang sama. Key partition tidak menyerialkan
eksekusi pool atau recovery scan pada replica lain.

**Revisi yang diusulkan:** durable claim/lease dengan owner atau fencing token,
expiry dan guard finalisasi. Claim mengubah eligibility sampai dilepas/expired.
Tetapkan timeout provider versus lease, renewal jika perlu, dan perlakuan hasil
worker lama setelah lease hilang. Local mutex saja tidak cukup lintas replica.
Bedakan penolakan stale DB writes dari duplicate push eksternal: fencing DB
sendiri tidak membatalkan request yang sudah diterima provider.

**Kasus wajib:** timer, recovery scan dan Kafka replay bersamaan hanya memberi
satu owner aktif. Uji worker berhenti lama, lease expired, takeover, dan hasil
lama datang setelah takeover. Dokumentasikan residual duplicate window.

## D3 — Blocker: recovery scan tidak mencakup semua state yang harus difinalisasi

**Bukti:** ADR-01 A-1 memilih zero-row, due retry dengan budget/deadline masih
valid, atau all-terminal rows. Tidak ada cabang eksplisit PENDING dengan
deadline expired / budget habis. A-10 memanggil finalize hanya setelah entry.

**Dampak:** crash setelah claim attempt terakhir atau selama menunggu retry
dapat meninggalkan PROCESSING+PENDING selamanya; tidak memenuhi klaim
"tidak stranded". Lease aktif juga perlu mencegah scan mengambil pekerjaan
yang masih dimiliki worker.

**Revisi yang diusulkan:** scan mencakup expired lease, TTL expired, budget
exhausted, dan all-terminal (termasuk DELIVERED/OPENED setelah ADR-05).
Pilih finalize/retry secara eksplisit dan idempotent. Tentukan timestamp acuan,
state sebelum deadline dibuat, serta lease yang masih aktif. Batch/pagination
dan fairness scan harus membatasi beban serta menghindari starvation.

**Kasus wajib:** restart setelah attempt terakhir diklaim tetapi belum
difinalisasi; deadline lewat saat worker mati; SENT berubah menjadi OPENED
sebelum campaign finalization. Semua mencapai state akhir tanpa operator.

## D4 — Blocker: sebagian PENDING rows belum membuktikan fan-out lengkap

**Bukti:** ADR-01 A-10 mengatakan upsert PENDING sebelum provider dan entry
PENDING berarti outstanding set. A-1 memperbolehkan commit ketika PENDING
tersimpan. Tidak ada batas transaksi atau marker fan-out complete yang
membedakan sebagian insert dari seluruh audience yang sudah tersnapshot.

**Dampak:** crash di tengah materialisasi audience dapat membuat recovery
mengirim sebagian perangkat lalu menganggap campaign selesai. Perubahan
audience/token setelah crash juga dapat mengubah sasaran retry.

**Revisi yang diusulkan:** pilih atomic snapshot/fan-out transaction untuk
skala yang dibuktikan, atau paginated durable fan-out dengan cursor dan marker
completion. Jangan mengizinkan submission/finalization/ack sebelum kondisi
durability yang dipilih terpenuhi. Tetapkan snapshot timing, perangkat eligible,
pergantian pemilik token dan penghapusan device selama retry.

**Kasus wajib:** hentikan proses setelah sebagian intent ditulis; restart
tetap menghasilkan seluruh audience awal, tanpa duplicate rows dan tanpa
campaign premature COMPLETED. Uji token refresh/account switch di tengah retry.

## D5 — Blocker: idempotency result dapat terlihat sebelum pekerjaan diterima

**Bukti:** ADR-01 A-8 menulis key+snapshot sebelum claim dan menghapus key saat
publish gagal. ADR-03 relationship section menghapus publish rollback, tetapi
masih menempatkan key upsert sebelum transaksi claim+outbox.

**Dampak:** request kedua dapat memperoleh replay 202 untuk key yang sudah
terlihat walau claim/outbox request pertama gagal atau belum commit. Kontrak
"202 = durably accepted" tidak terpenuhi. Aturan lama dan baru juga berbeda.

**Revisi yang diusulkan:** key reservation/result, DRAFT->QUEUED dan outbox
harus memiliki transaksi/serialisasi yang menjamin replay tidak mengembalikan
sukses sebelum durable acceptance. Pilih transaksi tunggal atau lifecycle key
yang eksplisit dengan handling request in-progress. Selaraskan ADR-01 A-8
dengan ADR-03 dan otorisasi replay; jangan hanya meninggalkan reconciliation
note pada dokumen lain.

**Kasus wajib:** request kedua membaca key ketika request pertama belum
commit; rollback/crash request pertama tidak meninggalkan replay sukses palsu.

## D6 — Should-fix: asumsi performa dan budget tidak sesuai kebutuhan terbaru

**Bukti:** ADR-01 A-9 memakai 100 campaign/detik dan burst 1.000/detik untuk
pool=32. Pengguna mengonfirmasi 20.000 perangkat aktif dan sekitar 5 campaign
per bulan. ADR-02 A-1 menyatakan stall sekitar 15 detik berdasarkan backoff,
tetapi total invocation time dan ketidaktersediaan DB untuk dead-letter belum
memiliki batas yang lengkap. Alasan "no herd" mengabaikan beberapa replica.

**Revisi yang diusulkan:** sizing berdasarkan device submissions/detik,
latensi batch, DB cost, provider quota dan recovery load. Bedakan concurrency
dari rate limit. Tetapkan per-operation deadline dan total retry budget;
pertimbangkan jitter untuk error bersama lintas replica. Jangan mengklaim
15 detik sebagai batas total atau worker count sebagai pembatas kuota.

**Kasus wajib:** full broadcast, dua campaign bertumpuk, retry storm, dan
provider slowdown tetap mempertahankan memory/concurrency/rate bounds.

## D7 — Should-fix: deterministik key bukan kontrak urutan replay

**Bukti:** ADR-02 A-2 menyatakan requeue memakai original key menjaga order;
producer `PublishSendRequested` membentuk ulang key dari notification ID.
Requeue membuat record baru di ujung log, tidak mengembalikan posisi asli.
Mapping partition juga bergantung pada topology/partitioner yang tetap.

**Revisi yang diusulkan:** nyatakan requeue sebagai replay event baru dengan
dedup di DB; jangan menjanjikan historical order. Tetapkan validasi decoded
event terhadap notification ID/key, dedup dead-letter berdasarkan source
topic/partition/offset, dan perlakuan crash setelah DLQ insert sebelum commit.
Pertahankan informasi replay/resolution untuk audit sesuai retention policy.
Jangan menjadikan APNs/FCM duplicate prevention sebagai asumsi tanpa kontrak
provider yang diverifikasi.

**Kasus wajib:** replay setelah campaign terminal no-op; DLQ insert sukses
lalu crash sebelum commit tidak menghasilkan quarantine duplikat tak terbatas.

## Batas review dan langkah berikutnya

- Ini review terhadap proposed design, bukan hasil tes concurrency/runtime.
- Klaim interface/provider eksternal lainnya belum diaudit lengkap.
- D1-D7 sudah memiliki respons rancangan pada tabel tindak lanjut; tidak ada
  temuan runtime yang ditutup hanya karena dokumen direvisi.
- ADR-01/02/03 perlu dicek kembali bersama ADR-04/05, terutama schema token,
  lease guard pada delivery update, event identity/ordering, dan outbox drain.
- Matriks kasus desain sudah tersedia di akhir ADR-01/02/03; pasangan SPEC
  tetap harus menetapkan query, file, migration, konfigurasi dan tests yang tepat.
