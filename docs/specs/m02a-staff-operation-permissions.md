# SPEC M02a — Staff operation permissions

Tanggal: 2026-11-02. Status: implemented and locally verified for M02a;
[acceptance evidence](../analysis/m02a-verification.md). Belum di-deploy.
Role matrix dikonfirmasi pengguna; ADR-11 keseluruhan tetap proposed dan
session/enrollment/recovery/security gates lain tidak otomatis diterima.

Sources: [ADR-11 A-4](../adr/adr-11-security-baseline.md),
[ADR-09](../adr/adr-09-api-consistency.md),
[CR1–CR9](../analysis/adr-01-11-consistency-review.md),
[machine-readable policy](../contracts/staff-permissions.json).

## 1. Scope dan evidence existing

M02a menutup operation permission bypass C21 pada notification, segment dan
segment-member routes/services. Ia juga memeriksa identity/active status untuk
read paths tersebut. Policy JSON adalah sumber matrix/route map untuk review
dan test fixtures; bukan runtime configuration yang dapat diubah client.
HTTP auth tetap API key + staff access JWT. Success DTO/status/state guards
existing tidak diganti oleh slice ini. Tidak ada endpoint baru, migration
schema, permission-management UI atau automatic provider send.

| Evidence sebelum implementasi | Historical condition |
| --- | --- |
| `cmd/server/main.go`, kedua `routes.go` | API-key/JWT/audit chain tanpa operation-role gate untuk 14 routes |
| `notifications/service.go`: Create/Update/Delete/Send | Membaca actor row tanpa pemeriksaan role atau IsActive eksplisit; JWT middleware memeriksa active di HTTP, tidak melindungi direct service callers |
| `segments/service.go`: Create/Update/Delete | Pola actor lookup serupa; interface mocks tersedia |
| `segments/members_service.go`: AddMember/RemoveMember | Tidak memeriksa staff actor/permission; memakai repository concrete |
| `db/queries/staff_users.sql`: GetStaffUserByID | Tidak memfilter is_active; menemukan row bukan bukti account aktif |
| `middleware/super_admin.go` | Legacy gate memakai RoleName JWT; bukan current-authority gate untuk feature ini |
| Notification CreateParams dan segment CreateParams | CreatedBy dapat berbeda dari actor bila service dipanggil langsung |
| `members_handler.go` | Tiga unknown-500 branches masih mengirim err.Error(); harus aman ketika authorization error ditambahkan |
| `notifications.Update` dan UpdateNotification SQL | PATCH scheduled_at mengganti timestamp, tetapi tidak mengubah status DRAFT menjadi SCHEDULED |

Tabel di atas adalah snapshot pre-implementation. C21 kini diremediasi oleh
code/tests, bukan oleh penulisan SPEC; lihat evidence. NEW di bawah menandai
artifact yang ditambahkan slice ini.

## 2. Policy yang dikonfirmasi dan route map

| Operasi | VIEWER | OPERATOR | ADMIN | SUPER_ADMIN |
| --- | --- | --- | --- | --- |
| Existing permitted notification/segment/member reads | Yes | Yes | Yes | Yes |
| Notification draft create/edit/delete | No | Yes | Yes | Yes |
| Segment dan membership create/edit/delete | No | Yes | Yes | Yes |
| Send dan persist non-null scheduling input | No | No | Yes | Yes |
| Staff/role administration (target policy, outside first slice) | No | No | No | Yes |

Matrix berlaku pada existing shared admin resource scope. CreatedBy adalah
provenance actor, bukan penambahan ACL owner-only atau tenancy. Slice tidak
memperluas business reads atau mengubah data-redaction policy. Jika dibutuhkan
team/tenant/creator-specific visibility, itu memerlukan kontrak object scope
dan query filter tersendiri sebelum scope tersebut diaktifkan.

Map seluruh 14 method/path ada di policy JSON. Required permission endpoint
dan field permission bersifat **AND**: notification create/update dengan
ScheduledAt non-nil membutuhkan draft.write **dan** schedule. Role/operation
baru, nama yang tidak dikenal, casing berbeda, role row kosong atau lookup
yang gagal tidak pernah memberi izin. Jangan menganggap role ID 1 selalu
SUPER_ADMIN atau mempercayai RoleName yang masih tersimpan di JWT.
HEAD mewarisi GET/read permission melalui ServeMux existing; method yang tidak
didukung tetap menggunakan 404/405 existing, bukan mendapat izin baru.

## 3. Current-authority contract

NEW internal authorization component menerima verified staff identity dari
context dan operation identifier yang ditetapkan kode server. Ia membaca
actor ID, IsActive dan current role name melalui satu SQL statement snapshot,
bukan memakai claim role atau melakukan dua lookup yang dapat mencampur versi.
Query NEW berada di `db/queries/staff_users.sql`, lalu sqlc diregenerasi; tidak
ada kolom baru sehingga tidak memerlukan migration schema.

Return principal hanya memuat ID dan current role yang diperlukan; jangan
menambahkan hash password/email ke DTO/cache/log authorization. Missing actor,
ID nonpositive, wrong context type/typed-nil claims, actor tidak ada atau inactive menghasilkan
unauthenticated denial. Existing unknown role menghasilkan forbidden denial.
DB/role integrity failure menghasilkan error internal terbungkus, tidak
disamarkan sebagai invalid credential atau 404. Cancellation/deadline tidak
menghasilkan successful authorization atau background mutation.

Komponen konkret adalah internal/authz.Authorizer, Checker.Require dan
AuthorityRepository.GetStaffAuthority. Semua service bergantung pada interface
Checker untuk menguji decision tanpa PostgreSQL.
Implementasi production memakai repository/sqlc existing conventions; module
constructor wajib memasang checker nyata, tanpa nil/allow-all fallback.

Route guard menolak operasi dasar sebelum handler business I/O. Service tetap
mengecek current authority saat entry, termasuk pemanggilan tanpa HTTP. Route
decision/context cache bukan otorisasi yang boleh melewati service check.
Demotion/offboarding yang committed sebelum service statement terlihat pada
statement itu, walau token masih memuat role lama. Promotion juga memakai role
DB terbaru, tidak memerlukan pembaruan claim role agar gate feature ini lolos.

**Batas race:** statement-time check belum menahan principal/policy lock sampai
commit mutation. Slice ini tidak menjanjikan pembatalan operasi yang sudah
diotorisasi ketika revocation berikutnya commit. Atomic current-authority check,
mutation/audit dan concurrency tests mengikuti transaksi pemilik data M03/M04
dan protokol CR1; acceptance ADR-11/production tetap menunggu itu. Checker harus
dapat dipakai dengan transaction-bound query, tanpa mutasi shared pool/global
state per request. Jangan memperkenalkan service↔outbox dependency cycle hanya
untuk memasang permission gate pertama.

## 4. Privileged inputs dan write boundaries

- **Create notification:** ScheduledAt nil menghasilkan draft path; non-nil
  timestamp memerlukan schedule permission sebelum target/template lookup dan
  insert. Masa lampau tetap validation error untuk actor yang authorized.
- **PATCH notification:** non-nil ScheduledAt juga memerlukan schedule, walau
  service saat ini hanya menyimpan timestamp. Jangan memperbaiki state transition
  secara diam-diam di permission slice. Durable scheduling/deadline semantics
  adalah M04. Existing DRAFT-only edit/delete/send guards tetap wajib.
- **Null/omission compatibility:** v1 pointers memperlakukan absent/null sebagai
  nil. Create null tetap draft; PATCH null tetap no-op untuk field tersebut,
  bukan clear/cancel schedule. Null tidak menjadi bypass untuk menyimpan
  timestamp. Presence-aware merge PATCH/clear behavior milik SPEC berbeda.
- **Creator provenance:** notification/segment create memakai ID principal
  yang diotorisasi. Handler/client/internal CreateParams.CreatedBy tidak boleh
  memalsukan actor; field lama dapat dipertahankan untuk source compatibility,
  tetapi bukan authority. Audit actor dan stored creator harus konsisten.
- **Member service:** AddMember/RemoveMember wajib melalui guard yang sama;
  permission tidak hanya dipasang di SegmentService. Reads juga memeriksa
  corresponding read permission sebelum resource lookup.
- **Denial:** tidak ada target/domain lookup yang membocorkan existence setelah
  operation denial, tidak ada business write, publish, status change atau success
  audit. Redacted denial telemetry diperbolehkan; jangan mencatat JWT/API key.

HTTP input parsing yang tidak menyentuh business state dapat menghasilkan
400 sebelum content-dependent schedule decision. Route-level denial untuk
operasi dasar tidak perlu memproses JSON. Tests policy memakai valid paths/
bodies; tidak mengandalkan perbedaan malformed input untuk memberi izin.

## 5. HTTP/error/compatibility contract

API key dan Bearer JWT tetap keduanya diperlukan. Role permission bukan
pengganti autentikasi. Untuk M02a gunakan envelope existing, tanpa tambahan
error-code field, ETag, 428, atau perubahan response success:

| Kondisi baru | Status / message |
| --- | --- |
| Missing/invalid staff identity atau inactive | 401; `authentication required` pada new permission gate; existing API-key/JWT messages tetap safe |
| Active known identity tidak punya operation/field permission | 403; `insufficient permissions` |
| Current-authority DB/integrity error atau unknown internal error | 500; literal `internal server error` |
| Authorized request gagal domain validation/state/resource rule | Existing mapped 400/404/409; tidak diubah menjadi permission success |

Contoh denial: `{"success":false,"message":"insufficient permissions"}`.
Tidak ada `data`, role database, stack, SQL/DSN atau arbitrary err.Error() pada
new denial/500 response. Wrapped authorization sentinels tetap dimapping dengan
errors.Is. List/detail/member handlers harus memetakan error baru juga; tidak
cukup menambah switch pada Create/Send. Perbaiki tiga hold-out unknown-500
branches members agar guard DB failure tidak bocor.

Ini security tightening pada existing v1 routes; jangan menambahkan toggle
yang mengembalikan VIEWER mutation access. Frontend harus mengandalkan response
status dan current permission, tidak menyembunyikan tombol sebagai satu-satunya
kontrol. UI dan backend mobile tidak diimplementasikan di repo ini. Existing
legacy send 202 bukan bukti durable outbox; durability contract berubah di M04.

## 6. Delivery slices dan verification

1. Shared checker + SQL query/repository + matrix unit tests, termasuk unknown
   operations dan shuffled role IDs. Contract tests mencocokkan seluruh role
   set dan operation/route map JSON dengan Go policy; tidak self-assert allow-all.
2. Wire notification/segment/member route dan service guards; adapt constructors,
   fixtures dan mocks bila interface berubah. Preserve service errors/state
   rules. Document HTTP behavior dengan target/runtime status yang jelas.
3. Tambahkan tagged real-DB/API tests ke dependency harness: semua empat role,
   stale claim/live role, direct service calls, scheduling bypass dan no-side-
   effect denials. Jangan menjalankan real worker/provider. Periksa row counts,
   creator/audit IDs dan Kafka absence, bukan status HTTP saja.

M02a selesai hanya jika matrix dan seluruh acceptance cases relevan lulus,
build/default tests/tagged tests/vet/format lulus, API docs updated, constructor
wiring fail closed, dan code-review C21 diperbarui dengan evidence/remaining
race limitations. SPEC ditulis bukan bukti semuanya sudah berjalan.

Staff administration service checks, session/JWT revocation, BFF/gateway
perimeter, DTO/redaction, broad body/rate limits, full API OpenAPI contract,
resource-error correctness dan atomic success audit tetap pekerjaan M02 lanjutan
atau transaksi milestone pemiliknya. USER/mobile authorization, enrollment dan
operational step-up tidak otomatis mendapat izin dari role matrix ini.

## Acceptance cases

| ID | Input / condition | Expected result / side effect | Expected error |
| --- | --- | --- | --- |
| A01 | Each role × each policy permission | Exact allow/deny matrix; unknown/case variant never allowed | Forbidden if denied |
| A02 | Every mapped method/path | Correct route guard; POST/PATCH extra schedule rule applied | Per matrix |
| A03 | Missing/wrong context, typed-nil claims, ID <=0, deleted/inactive actor; direct service invocation too | No domain write/publish/success audit; HTTP 401 | Unauthenticated |
| A04 | Actor authority query fails or role row integrity absent | Fail closed, generic 500, no write; never fake 401/404 | Wrapped internal error |
| A05 | Token says SUPER_ADMIN, DB now VIEWER | Business reads allowed, every one of nine mutation routes denied; DB unchanged | 403 |
| A06 | Token says VIEWER, DB now ADMIN | Feature gates use current DB authority; normal state/validation still required | None from role gate |
| A07 | VIEWER/unknown role calls create/edit/delete/send or membership writes | Uniform operation denial for existing/unknown valid IDs; no domain lookup/publish/write | 403 |
| A08 | OPERATOR creates/edits/deletes draft; manages segment/members | Allowed subject to existing domain guards; real actor attributed | Existing domain errors only |
| A09 | OPERATOR sends, creates with future ScheduledAt or PATCHes non-null ScheduledAt | All denied; no scheduling field/status/target/audit/Kafka change | 403 |
| A10 | ADMIN/SUPER_ADMIN same valid operations | Role gate allows; existing DRAFT/domain rules still enforced | None from role gate |
| A11 | OPERATOR ScheduledAt absent/null on create or title PATCH | No schedule persistence/clear side effect; legacy draft/no-op behavior retained | Existing validation only |
| A12 | Authorized actor supplies CreatedBy of another staff in direct CreateParams | Stored/audit creator is verified actor; no impersonation | None from permission gate |
| A13 | Demotion between route gate and service check | Service independently rechecks committed authority and denies mutation | 403 |
| A14 | Context canceled/deadline during authority lookup | No later write/publish or async permission success | Context/internal error |
| A15 | Wrapped denial/internal error reaches every affected handler | Safe 401/403/500 envelope; no raw details, including member handlers | Mapped error |
| A16 | Known authorized read/write fixtures | Existing success body/status and domain guards preserved; no new owner-only rule | Existing domain errors only |
| A17 | Real DB with shuffled role IDs | Authority determined by joined name/active state, not numeric hierarchy or token role | Per matrix |
| A18 | Existing unit + tagged smoke suites | No skipped assertions/allow-all production fallback; default tests remain credential-free | None |
