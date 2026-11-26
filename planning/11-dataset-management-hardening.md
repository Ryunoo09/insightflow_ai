# Plan 11: Dataset Management & Queue Hardening (Plan 04 Debt)

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-011 |
| Name | Plan 04 Debt Repayment: Queue Recovery, Profile Timeout, Dataset CRUD, List Query, Public Config |
| Priority | P1 – High (sebagian P0 untuk ketahanan antrean) |
| Complexity | Large |
| Status | Planned |
| Depends On | PLAN-04, PLAN-10 |
| Related Requirements | FR-DS-06, FR-DS-08, FR-DS-11, F-11, SEC-RETENTION, SEC-REDIS |
| Additive To | Plan 04 (TASK-401..405), Plan 05 (list dataset, bukan explorer baris) |

### 1.1 Objective

Melunasi utang Plan 04 yang masih terbuka: job yang macet setelah crash, timeout profiling yang belum ada, test boundary yang belum ditulis, fitur manajemen dataset (rename, soft delete, purge, reprocess), query daftar dataset server-side, dan endpoint konfigurasi publik.

Plan ini **tidak mengubah antrean**: `processing_jobs` tetap sumber kebenaran; Plan 10 tetap satu-satunya sinyal bangun Redis.

### 1.2 Scope

**In Scope:**
- Reaper job `processing` yang melewati batas `locked_at` (crash/timeout), requeue atau fail permanen.
- Watchdog timeout 30 detik untuk profiling DuckDB via `con.interrupt()`.
- Test HTTP handler upload (202, 413, signature salah, zip bomb, path isolation).
- Test klaim konkuren dua worker + test reaper.
- Rename dataset, soft delete + purge janitor, dan nama unik per workspace (`DATASET_NAME_TAKEN`).
- Reprocess menjadi versi baru, retensi maksimal 3 versi.
- Daftar dataset: paginasi, pencarian nama, filter status, pengurutan server-side (FR-DS-06).
- `GET /api/v1/config/limits` publik untuk batas upload/kuota dan status Google.
- Frontend: halaman `/datasets/new`, kontrol daftar, aksi rename/reprocess/delete.
- Perbaikan traceability roadmap (F-11 tidak lagi menunjuk TASK-503).

**Out of Scope:**
- Override kolom (FR-DS-10) — reprocess di plan ini memakai skema yang sama; override menyusul.
- Stress 50 MB CSV / 20 MB XLSX — sudah dicakup Plan 09 TASK-902.
- Perubahan encoding CSV — implementasi sekarang sudah sesuai PRD (Windows-1252 diterima).
- Explorer baris (FR-DS-09) — Plan 05.
- Audit log emission untuk upload/delete/reprocess — hook disiapkan, pencatatan penuh di Plan 08.
- Workspace rename/delete — di luar Plan 04.

### 1.3 Requirements Traceability

| Requirement ID | Deskripsi | Rencana Implementasi | Sumber |
|---|---|---|---|
| F-08/F-09 | Worker idempoten; job tidak boleh menggantung | Reaper `locked_at` + requeue/fail | PRD 27.10, Architecture 4.6 |
| FR-DS-06 | Daftar dataset: paginasi, cari nama, filter status, urut | TASK-1107 | PRD Bagian 19 |
| FR-DS-08 | Soft delete lalu purge fisik setelah 7 hari | TASK-1105 | PRD Bagian 19 & 27.10 |
| FR-DS-11 / F-11 | Reprocess membuat versi baru; simpan maks. 3 versi | TASK-1106 | PRD Bagian 19 & 24 |
| `DATASET_NAME_TAKEN` | Nama dataset unik per workspace | TASK-1105 | PRD katalog error |
| `DATASET_RESOURCE_EXHAUSTED` | Profiling melebihi batas sumber daya/waktu | TASK-1102 | PRD katalog error |
| K-05 | Profiling 100k × 20 kolom ≤ 30 detik | TASK-1102 | PRD metrik |
| `/config/limits` | Batas publik untuk halaman login/upload | TASK-1108 | PRD Bagian 10 & 18 |

---

## 2. Technical Context

Kondisi setelah Plan 04 + Plan 10:

```
upload → COMMIT dataset+version+job → LPUSH jobs:wake → 202
worker → ClaimNextJob (SKIP LOCKED, locked_at=now()) → Engine
crash setelah claim → status tetap 'processing', locked_at lama, tidak ada yang mengembalikan
profile → DuckDB tanpa timeout; query panjang menahan worker
list dataset → SELECT semua baris, filter di browser
rename/delete/reprocess → belum ada endpoint
nama dataset → tidak ada unique constraint
```

Yang sudah tersedia dan dipakai ulang:
- `processing_jobs.locked_at` sudah ada dan diisi `ClaimNextJob`.
- `datasets.current_version_no`, `dataset_versions.version_no` sudah mendukung versi baru.
- `StorageResolver` sudah membangun path per versi; file Parquet immutable.
- `TxManager`, sqlc, `apperr`/`httpx`, dan pola handler/service/repository sudah ada.

### 2.1 Keputusan desain

| Keputusan | Alasan |
|---|---|
| Reaper memakai `locked_at < now() - JOB_STALE_AFTER` (default 10 menit) | Lebih besar dari timeout Engine 300 detik; worker yang masih sehat tidak direbut |
| Reaper requeue bila `attempts < max_attempts`, selain itu fail permanen | Mencegah loop tanpa akhir; versi ikut `failed` |
| Timeout profil memakai `con.interrupt()` dari thread pengawas | DuckDB tidak punya `statement_timeout`; ini cara resmi membatalkan query |
| Soft delete `deleted_at` + `purge_after`; purge file dulu, baris kemudian | Aman terhadap kegagalan sebagian; idempoten |
| Nama unik parsial `lower(name)` per workspace, hanya baris hidup | Rename/delete tidak memblokir nama lama; `DATASET_NAME_TAKEN` 409 |
| Reprocess menyalin `source_path` versi terakhir, bukan upload ulang | File sumber immutable; hemat bandwidth |
| Retensi 3 versi dijalankan saat reprocess sukses dibuat | Menghindari janitor tambahan untuk versi lama |
| Daftar dataset memakai SQL dinamis di repository dengan allowlist sort | sqlc statis tidak nyaman untuk sort dinamis; query tetap parameterized |
| `/config/limits` tanpa auth, hanya nilai publik | FE butuh sebelum login; tidak boleh memuat secret |

### 2.2 Perubahan perilaku antrean

`ClaimNextJob` ditambah syarat dataset belum dihapus:

```sql
JOIN datasets d ON d.id = dv.dataset_id
WHERE ... AND d.deleted_at IS NULL
```

Job untuk dataset yang di-soft-delete tidak akan pernah diklaim lagi.

---

## 3. Implementation Tasks

### Task ID: TASK-1101
**Task Name:** Stale Processing Job Reaper
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-04, PLAN-10 |
| Related Requirements | F-08, F-09 |

**Objective:**
Mengembalikan job yang tertahan di `processing` karena crash/timeout ke antrean, atau menandainya gagal permanen bila percobaan habis.

**Implementation Steps:**
1. Migration `000003_dataset_management` menambah index parsial:
   `CREATE INDEX processing_jobs_stale_idx ON processing_jobs (locked_at) WHERE status = 'processing';`
2. Tambah query `RequeueStaleJobs` (satu statement, `UPDATE ... RETURNING id, version_id, attempts, max_attempts`) dengan batas `locked_at < now() - $1::interval`.
3. Pisahkan hasil di Go:
   - `attempts < max_attempts` → `status='queued'`, `locked_at=NULL`, `run_after=now()`, `error_message='stale job requeued'`.
   - `attempts >= max_attempts` → `status='failed'`, `finished_at=now()`, lalu `datasets.Fail(versionID, "DATASET_RESOURCE_EXHAUSTED", "pemrosesan melewati batas waktu")`.
4. Jalankan reaper tiap 60 detik dari `jobs.WorkerPool` atau goroutine terpisah di `main.go`; berhenti saat context shutdown.
5. Konfigurasi `JOB_STALE_AFTER` (default `10m`), validasi > timeout Engine (300s).

**Expected Files:**
- `apps/api/db/migrations/000003_dataset_management.up.sql`
- `apps/api/db/migrations/000003_dataset_management.down.sql`
- `apps/api/db/queries/jobs.sql`
- `apps/api/internal/platform/dbgen/jobs.sql.go`
- `apps/api/internal/modules/jobs/repository.go`
- `apps/api/internal/modules/jobs/reaper.go`
- `apps/api/cmd/api/main.go`

**Testing Requirements:**
- Klaim job, backdate `locked_at`, jalankan reaper → status `queued`, `locked_at NULL`.
- `attempts = max_attempts` → status `failed` dan versi `failed`.
- Job `processing` yang masih baru (locked_at sekarang) tidak disentuh.
- Reaper idempoten: dua pemanggilan tidak menggandakan aksi.

**Acceptance Criteria:**
- Tidak ada job `processing` yang tertahan lebih dari `JOB_STALE_AFTER` tanpa tindakan.

---

### Task ID: TASK-1102
**Task Name:** DuckDB Profiling Watchdog Timeout
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Small |
| Status | Planned |
| Dependencies | PLAN-04 |
| Related Requirements | K-05, DATASET_RESOURCE_EXHAUSTED |

**Objective:**
Membatalkan profiling yang melewati 30 detik dan mengembalikan error yang dapat dipetakan Go/UI.

**Implementation Steps:**
1. Tambah parameter `timeout_seconds` pada `profile_parquet` (default dari settings, 30).
2. Jalankan `threading.Timer(timeout, con.interrupt)` sebelum query; `timer.cancel()` di `finally`.
3. Tangkap `duckdb.InterruptException` dan lempar `EngineError("DATASET_RESOURCE_EXHAUSTED", "profiling melebihi batas waktu", retryable=False)`.
4. Tambah `PROFILE_TIMEOUT_SECONDS` di config Engine + `.env.example`.
5. Pastikan API `/v1/datasets/process` memetakan error itu ke status non-retryable.

**Expected Files:**
- `apps/engine/app/profiling/profiler.py`
- `apps/engine/app/config.py`
- `apps/engine/app/data_engine/errors.py`
- `apps/engine/app/api/v1/datasets.py`
- `.env.example`

**Testing Requirements:**
- Test dengan `timeout_seconds` sangat kecil dan query berat → `DATASET_RESOURCE_EXHAUSTED`.
- Test profiling normal tetap selesai dan timer dibatalkan.
- Test tidak ada thread tertinggal setelah sukses/gagal.

**Acceptance Criteria:**
- Profiling tidak pernah berjalan lebih dari batas + toleransi kecil; worker tidak tertahan.

---

### Task ID: TASK-1103
**Task Name:** Upload Handler HTTP Test Suite
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-04 |
| Related Requirements | SEC-UPLOAD, FR-DS-01..04 |

**Objective:**
Menguji jalur HTTP nyata: multipart streaming, batas ukuran, signature, dan path isolation.

**Implementation Steps:**
1. Buat `upload_handler_test.go` dengan `httptest.NewRecorder` dan body multipart.
2. Siapkan DB test + `t.TempDir()` storage + router/handler dengan konteks auth workspace.
3. Kasus: CSV valid → 202 + `dataset_id` + file ada + sha256 cocok; XLSX valid → 202.
4. Kasus: file > batas → 413 tanpa file tersisa; signature salah → 422; ZIP bomb → 422.
5. Kasus: nama file berisi `../`, NUL, karakter kontrol → tersimpan sebagai metadata aman; path tetap di bawah root.
6. Pastikan request gagal tidak meninggalkan file parsial atau baris dataset.

**Expected Files:**
- `apps/api/internal/modules/datasets/upload_handler_test.go`

**Testing Requirements:**
- Semua kasus di atas berjalan dengan `TEST_DATABASE_URL`.
- Cleanup file dan baris DB di `t.Cleanup`.

**Acceptance Criteria:**
- Handler upload tercakup test otomatis; tidak ada regresi pada validator/service.

---

### Task ID: TASK-1104
**Task Name:** Concurrent Claim & Reaper Integration Test
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-1101 |
| Related Requirements | F-08, F-09 |

**Objective:**
Membuktikan dua worker tidak pernah mengambil job yang sama dan reaper tidak merebut job aktif.

**Implementation Steps:**
1. Seed N job `queued` untuk N versi dataset.
2. Jalankan dua goroutine dengan dua `pgxpool` terpisah, masing-masing loop `ClaimNextJob`.
3. Kumpulkan `id` yang diklaim; assert tidak ada duplikat dan total = N.
4. Test reaper: satu job `processing` dengan `locked_at` baru tidak di-requeue; yang lama di-requeue.
5. Test kombinasi: worker A memegang job, reaper berjalan sebelum batas → job tetap `processing`.

**Expected Files:**
- `apps/api/internal/modules/jobs/concurrency_test.go`

**Testing Requirements:**
- Gunakan `TEST_DATABASE_URL`; bersihkan job/versi yang dibuat.
- Hindari `time.Sleep` panjang; backdate `locked_at` langsung via SQL.

**Acceptance Criteria:**
- Nol klaim ganda; nol job aktif direbut reaper.

---

### Task ID: TASK-1105
**Task Name:** Dataset Rename, Soft Delete, Purge, and Name Uniqueness
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Large |
| Status | Planned |
| Dependencies | PLAN-04 |
| Related Requirements | FR-DS-08, DATASET_NAME_TAKEN, SEC-RETENTION |

**Objective:**
Menambahkan rename, soft delete, purge fisik terjadwal, dan nama unik per workspace.

**Implementation Steps:**
1. Migration:
   - `datasets.deleted_at timestamptz NULL`, `datasets.purge_after timestamptz NULL`.
   - `CREATE UNIQUE INDEX datasets_workspace_name_key ON datasets (workspace_id, lower(name)) WHERE deleted_at IS NULL;`
   - Index `datasets_purge_idx ON datasets (purge_after) WHERE deleted_at IS NOT NULL;`
2. Query: `RenameDataset` (membership-checked), `SoftDeleteDataset`, `ListPurgeDue`, `PurgeDataset`, `GetDataset`/list mengecualikan `deleted_at IS NOT NULL`.
3. Endpoint:
   - `PATCH /api/v1/datasets/:dataset_id` body `{ "name": "..." }` → 200.
   - `DELETE /api/v1/datasets/:dataset_id` → 202; set `deleted_at=now()`, `purge_after=now()+7 days`, fail job queued/processing.
4. Map unique violation `datasets_workspace_name_key` → `409 DATASET_NAME_TAKEN`.
5. Purge janitor: ticker 1 jam; untuk tiap dataset due, hapus file source/parquet semua versi via resolver, lalu hapus baris dataset (cascade). File hilang = lanjut. Log tanpa path absolut.
6. `ClaimNextJob` mengecualikan dataset `deleted_at IS NOT NULL`.

**Expected Files:**
- `apps/api/db/migrations/000003_dataset_management.*`
- `apps/api/db/queries/datasets.sql`
- `apps/api/internal/modules/datasets/{handler,service,repository}.go`
- `apps/api/internal/modules/jobs/repository.go`
- `apps/api/cmd/api/main.go`

**Testing Requirements:**
- Rename sukses; rename ke nama dataset lain di workspace sama → 409.
- Nama sama di workspace berbeda → boleh.
- Soft delete: list/get 404, job tidak diklaim, purge_after terisi.
- Purge: file source+parquet hilang, baris hilang, idempoten saat dijalankan dua kali.
- Viewer tidak boleh rename/delete (`403`).

**Acceptance Criteria:**
- Nama unik ditegakkan DB; delete tidak meninggalkan file setelah purge.

---

### Task ID: TASK-1106
**Task Name:** Reprocess as New Version with 3-Version Retention
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Large |
| Status | Planned |
| Dependencies | TASK-1105 |
| Related Requirements | FR-DS-11, F-11 |

**Objective:**
Memproses ulang dataset dari file sumber yang sama menjadi versi baru, tanpa menimpa versi lama.

**Implementation Steps:**
1. Endpoint `POST /api/v1/datasets/:dataset_id/reprocess` (editor+).
2. Tolak bila versi aktif `queued`/`processing` → `409 DATASET_BUSY`.
3. Dalam satu transaksi:
   - Insert `dataset_versions` baru (`version_no = max+1`, status `queued`, copy `source_path`, `file_format`, `original_name`, `byte_size`, `checksum_sha256`).
   - Update `datasets.current_version_no`.
   - Enqueue job `dataset_ingest`.
4. Kirim wake signal setelah commit (pola Plan 10).
5. Setelah versi baru `ready`, hapus versi di luar 3 terakhir: file Parquet/source (file-first), lalu baris versi (cascade kolom/profil).
6. Job worker tetap memakai alur Plan 04; tidak ada perubahan kontrak Engine.

**Expected Files:**
- `apps/api/internal/modules/datasets/{handler,service,repository}.go`
- `apps/api/db/queries/datasets.sql`
- `apps/api/internal/modules/jobs/*` (retensi file)
- `apps/api/internal/router/router.go`

**Testing Requirements:**
- Reprocess membuat versi 2, job baru, versi 1 tetap `ready`.
- Dua reprocess bersamaan → satu sukses, satu `DATASET_BUSY`.
- Setelah versi 4 selesai, hanya versi 2–4 tersisa; file versi 1 terhapus.
- Analisis lama tetap menunjuk `dataset_version_id` lama (tidak berubah).

**Acceptance Criteria:**
- Versi lama tidak pernah ditimpa; retensi maksimal 3 versi ditegakkan.

---

### Task ID: TASK-1107
**Task Name:** Server-Side Dataset List Query
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-1105 |
| Related Requirements | FR-DS-06 |

**Objective:**
Memindahkan paginasi/pencarian/filter/sort daftar dataset dari browser ke PostgreSQL.

**Implementation Steps:**
1. `GET /api/v1/workspaces/:workspace_id/datasets?page=1&per_page=20&q=&status=&sort=updated_at&order=desc`.
2. Validasi: `page >= 1`, `per_page` 1–100 (default 20), `status` enum, `sort` allowlist (`name`, `created_at`, `updated_at`, `row_count`, `byte_size`, `status`), `order` `asc|desc`.
3. Query repository parameterized:
   - Filter `workspace_id`, `deleted_at IS NULL`, `name ILIKE $q` (escape `%`/`_`), `status`.
   - `ORDER BY` hanya dari map allowlist, bukan string user.
   - `COUNT(*)` terpisah untuk `total`.
4. Respons `{ "items": [...], "page": 1, "per_page": 20, "total": 42 }`.
5. Frontend memakai parameter URL agar state shareable.

**Expected Files:**
- `apps/api/internal/modules/datasets/{handler,repository}.go`
- `apps/api/internal/router/router.go`
- `apps/web/src/features/datasets/api.ts`
- `apps/web/src/features/datasets/pages/DatasetsPage.tsx`

**Testing Requirements:**
- Paginasi 20 per halaman, total benar.
- Pencarian case-insensitive, `%` tidak menjadi wildcard liar.
- Filter status dan sort valid; `sort` tak dikenal → fallback/default atau 422.
- Dataset soft-deleted tidak muncul; workspace lain tidak bocor.

**Acceptance Criteria:**
- Browser tidak lagi mengunduh seluruh daftar dataset.

---

### Task ID: TASK-1108
**Task Name:** Public Config Limits Endpoint
| Field | Description |
|---|---|
| Priority | P2 |
| Complexity | Small |
| Status | Planned |
| Dependencies | PLAN-03, PLAN-04 |
| Related Requirements | PRD Bagian 10 & 18 |

**Objective:**
Menyediakan batas publik yang dibutuhkan halaman login dan upload tanpa membocorkan secret.

**Implementation Steps:**
1. `GET /api/v1/config/limits` tanpa auth.
2. Respons: `upload.csv_max_bytes`, `upload.xlsx_max_bytes`, `dataset.max_per_workspace`, `dataset.max_rows_csv`, `dataset.max_rows_xlsx`, `dataset.max_columns`, `profile.timeout_seconds`, `auth.google_enabled`.
3. Ambil dari `config`; jangan sertakan URL internal, token, atau kredensial.
4. Frontend memakai nilai ini untuk teks batas dan validasi awal.

**Expected Files:**
- `apps/api/internal/router/router.go`
- `apps/api/internal/modules/config` (handler kecil) atau handler di router
- `apps/web/src/features/datasets/pages/DatasetsPage.tsx`

**Testing Requirements:**
- Respons 200 tanpa auth; tidak ada field sensitif.
- Nilai cocok dengan env yang dimuat.
- `google_enabled` false bila Google tidak dikonfigurasi.

**Acceptance Criteria:**
- FE tidak lagi hardcode batas upload.

---

### Task ID: TASK-1109
**Task Name:** Frontend Dataset Management UI
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-1105..1108 |

**Objective:**
Menambahkan halaman upload khusus dan aksi manajemen dataset.

**Implementation Steps:**
1. Route `/app/:workspaceId/datasets/new` dengan form nama + dropzone; redirect ke detail setelah 202.
2. Halaman list: search input, filter status, sort dropdown, pagination, tombol aksi per baris.
3. Aksi: rename (dialog), reprocess (konfirmasi), delete (dialog konfirmasi).
4. Detail: tombol Reprocess dan indikator versi aktif.
5. Tampilkan `DATASET_NAME_TAKEN` dan `DATASET_BUSY` sebagai pesan yang jelas.

**Expected Files:**
- `apps/web/src/features/datasets/pages/DatasetsPage.tsx`
- `apps/web/src/features/datasets/pages/DatasetNewPage.tsx`
- `apps/web/src/features/datasets/pages/DatasetDetailPage.tsx`
- `apps/web/src/features/datasets/api.ts`
- `apps/web/src/App.tsx`

**Testing Requirements:**
- `npm run build` bersih.
- Test komponen/route untuk upload baru, rename, delete, reprocess.
- Manual/E2E: upload → list → rename → reprocess → delete.

**Acceptance Criteria:**
- Semua aksi manajemen dataset tersedia dari UI tanpa curl.

---

### Task ID: TASK-1110
**Task Name:** Traceability and Documentation Cleanup
| Field | Description |
|---|---|
| Priority | P2 |
| Complexity | Small |
| Status | Planned |
| Dependencies | TASK-1101..1109 |

**Objective:**
Memperbaiki pemetaan requirement yang salah dan mencatat utang yang dilunasi.

**Implementation Steps:**
1. `planning/00-roadmap.md`: pisahkan F-10 (Plan 04 TASK-405) dan F-11 (Plan 11 TASK-1106); hapus referensi F-11 ke TASK-503.
2. Tambah Plan 11 ke diagram dan tabel roadmap.
3. `planning/README.md`: tambah baris indeks Plan 11.
4. Addendum di `planning/04` yang menunjuk Plan 11 untuk item yang belum dikerjakan.
5. `docs/core_docs/Architecture.md`/`Security.md`: catat reaper, purge file-first, dan timeout profil bila belum tertulis.
6. PRD tidak berubah.

**Expected Files:**
- `planning/00-roadmap.md`
- `planning/README.md`
- `planning/04-dataset-pipeline-profiling.md`
- `docs/core_docs/Architecture.md`
- `docs/core_docs/Security.md`

**Acceptance Criteria:**
- Tidak ada requirement yang menunjuk task yang tidak mengimplementasikannya.

---

## 4. API & Database Changes

### API Changes
| Method | Endpoint | Purpose | Auth |
|---|---|---|---|
| PATCH | `/api/v1/datasets/:dataset_id` | Rename dataset | Bearer JWT + editor |
| DELETE | `/api/v1/datasets/:dataset_id` | Soft delete dataset | Bearer JWT + editor |
| POST | `/api/v1/datasets/:dataset_id/reprocess` | Buat versi baru | Bearer JWT + editor |
| GET | `/api/v1/workspaces/:workspace_id/datasets` | List + paginasi/filter/sort | Bearer JWT + member |
| GET | `/api/v1/config/limits` | Batas publik | Publik |

### Database Changes
Migration `000003_dataset_management`:
- `datasets.deleted_at timestamptz NULL`
- `datasets.purge_after timestamptz NULL`
- Unique index `datasets_workspace_name_key` pada `(workspace_id, lower(name)) WHERE deleted_at IS NULL`
- Index `datasets_purge_idx` pada `purge_after WHERE deleted_at IS NOT NULL`
- Index `processing_jobs_stale_idx` pada `locked_at WHERE status = 'processing'`

Tidak ada perubahan pada `dataset_versions`, `dataset_columns`, `dataset_profiles`, atau kontrak Engine.

### Configuration Changes
- `JOB_STALE_AFTER` (default `10m`)
- `PROFILE_TIMEOUT_SECONDS` (default `30`)
- `DATASET_PURGE_GRACE_DAYS` (default `7`)

---

## 5. Design & UI Implementation

- `/app/:workspaceId/datasets/new`: form nama (default dari nama file), dropzone, info batas dari `/config/limits`, progress bar.
- `/app/:workspaceId/datasets`: toolbar pencarian + filter status + sort + pagination; menu aksi per baris.
- Detail dataset: tombol Reprocess, indikator versi (`v3`), dialog konfirmasi delete.
- Status/error: `DATASET_NAME_TAKEN` di bawah input nama; `DATASET_BUSY` pada tombol reprocess.
- Tidak ada perubahan design token.

---

## 6. Security Requirements

- Reaper tidak boleh mengeksekusi job yang masih dalam timeout Engine; default 10 menit > 300 detik.
- Purge menghapus file lebih dulu, lalu baris; jangan pernah menghapus baris lebih dulu.
- Path file selalu dari `StorageResolver`; nama dataset tidak pernah menjadi path.
- Unique index nama mencegah duplikasi lintas tenant; query tetap memeriksa membership.
- `/config/limits` tidak boleh memuat secret, URL internal, atau kredensial.
- Soft-deleted dataset tidak boleh muncul di list/get/explorer dan tidak boleh diklaim worker.
- Purge dan reprocess memakai transaksi/urutan yang idempoten; log tanpa path absolut.

---

## 7. Testing & Verification

- **Go:**
  - `cd apps/api; go build ./...; go vet ./...`
  - `go test ./internal/modules/datasets/... ./internal/modules/jobs/...`
  - Dengan `TEST_DATABASE_URL` dan `TEST_REDIS_ADDR`.
- **Engine:**
  - `cd apps/engine; pytest tests/test_profiler.py tests/test_datasets_api.py`
- **Web:**
  - `cd apps/web; npm test; npm run build`
- **Skenario wajib:**
  1. Job `processing` crash (backdate `locked_at`) → reaper requeue → selesai.
  2. Dua worker, N job → tidak ada klaim ganda.
  3. Profiling dibuat lambat → berhenti ~30 detik, versi `failed` dengan `DATASET_RESOURCE_EXHAUSTED`.
  4. Upload > batas → 413; signature salah → 422; file tidak tersisa.
  5. Rename ke nama yang dipakai → 409; nama sama di workspace lain → sukses.
  6. Delete → hilang dari list, file terhapus setelah purge.
  7. Reprocess → versi baru `ready`, versi lama tetap, maksimal 3 versi.
  8. List 45 dataset → halaman 20/20/5, search/filter/sort benar.
- **Regresi:** seluruh test Plan 04 + Plan 10 tetap lulus.

---

## 8. Commit Plan

### 8.1 Commit Strategy
Pisahkan ketahanan antrean, timeout engine, test boundary, skema manajemen dataset, reprocess, query list, config, UI, lalu dokumentasi.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-1101 | feat | jobs | feat(jobs): reap stale processing jobs and requeue or fail them |
| 02 | TASK-1102 | feat | engine | feat(engine): interrupt profiling after the configured timeout |
| 03 | TASK-1103 | test | datasets | test(datasets): cover the multipart upload handler end to end |
| 04 | TASK-1104 | test | jobs | test(jobs): prove concurrent claims and reaper safety |
| 05 | TASK-1105 | feat | datasets | feat(datasets): add rename, soft delete, purge, and unique names |
| 06 | TASK-1106 | feat | datasets | feat(datasets): reprocess datasets into new versions with retention |
| 07 | TASK-1107 | feat | datasets | feat(datasets): paginate, search, filter, and sort dataset lists |
| 08 | TASK-1108 | feat | config | feat(config): expose public upload and dataset limits |
| 09 | TASK-1109 | feat | web | feat(web): add dataset upload page and management actions |
| 10 | TASK-1110 | docs | planning | docs(planning): fix requirement traceability and record Plan 11 |

### 8.3 Commit Details
- **01:** migration index + query + reaper + test.
- **02:** watchdog timer + error mapping + test.
- **03:** handler upload tests (202/413/422/path).
- **04:** concurrent claim + reaper safety tests.
- **05:** schema, rename/delete/purge, name uniqueness, claim join.
- **06:** reprocess endpoint + retention cleanup.
- **07:** list query + frontend controls.
- **08:** `/config/limits`.
- **09:** `/datasets/new`, aksi rename/reprocess/delete.
- **10:** roadmap/README/Plan 04 addendum + docs.

### 8.4 Commit Boundaries
- Commit 05 mengubah skema; migration harus terdaftar di `apps/api/sqlc.yaml`.
- Commit 06 tidak boleh mengubah kontrak `/v1/datasets/process`.
- Commit 07 tidak boleh memakai string user langsung di `ORDER BY`.
- Commit 09 tidak boleh memuat batas hardcoded; ambil dari `/config/limits`.

### 8.5 Commit Dependencies
- 04 bergantung pada 01. 05 bergantung pada 01 (claim join). 06 bergantung pada 05. 07 bergantung pada 05. 09 bergantung pada 05–08. 10 terakhir.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Reaper merebut job yang masih berjalan | Dua worker memproses versi sama | Sedang | Default stale 10 menit > Engine 300s; heartbeat tidak dipakai |
| Reaper + retry membuat loop | Job terus requeue | Rendah | `attempts >= max_attempts` → failed permanen |
| Purge gagal di tengah | File/baris tersisa | Sedang | File-first, idempoten, ulangi pada tick berikutnya |
| Unique index gagal dibuat karena data lama duplikat | Migration gagal | Rendah | Cek duplikat sebelum migration; dev data bisa dibersihkan |
| Reprocess bersamaan | Dua versi baru | Sedang | Transaksi + cek versi aktif `queued/processing` → `DATASET_BUSY` |
| Retensi 3 versi menghapus versi yang dipakai analisis | Referensi analisis rusak | Rendah | Analisis menyimpan `dataset_version_id`; versi lama dihapus hanya saat reprocess dan sudah tidak aktif; dokumentasikan |
| Sort dinamis membuka SQL injection | Data leak/error | Rendah | Allowlist map, parameterized filter, test sort tak dikenal |
| Purge menghapus file milik versi yang masih dipakai | Data hilang | Rendah | Hanya dataset soft-deleted + purge_after lewat; versi aktif tidak dihapus |
| Timeout 30 detik memotong dataset besar yang valid | Versi gagal | Sedang | Batas PRD K-05; tampilkan pesan jelas; naikkan via env bila perlu |

---

## 10. Definition of Done

- [ ] Tidak ada job `processing` yang tertahan lebih dari `JOB_STALE_AFTER`.
- [ ] Profiling berhenti ≤ 30 detik + toleransi; error `DATASET_RESOURCE_EXHAUSTED` tercatat.
- [ ] Handler upload punya test otomatis untuk 202/413/422/path isolation.
- [ ] Test dua worker membuktikan nol klaim ganda; reaper tidak merebut job aktif.
- [ ] Rename, soft delete, purge, dan nama unik bekerja; `DATASET_NAME_TAKEN` 409.
- [ ] Reprocess membuat versi baru; versi lama tetap; maksimal 3 versi.
- [ ] Daftar dataset memakai paginasi/pencarian/filter/sort server-side.
- [ ] `/config/limits` publik tanpa secret; FE memakainya.
- [ ] Halaman `/datasets/new` dan aksi manajemen tersedia di UI.
- [ ] Seluruh test Plan 04 + Plan 10 tetap lulus; `go build`, `go vet`, `pytest`, `npm test`, `npm run build` bersih.
- [ ] Roadmap/README/Plan 04 traceability diperbaiki; PRD tidak berubah.
