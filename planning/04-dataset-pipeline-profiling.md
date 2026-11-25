# Plan 04: Dataset Ingestion, Job Pipeline & Data Profiling

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-004 |
| Name | Dataset Ingestion, Asynchronous Job Queue & Profiling Engine |
| Priority | P0 – Critical |
| Complexity | Large |
| Status | Planned |
| Depends On | PLAN-002, PLAN-003 |
| Related Requirements | F-06, F-07 (Upload & Storage), F-08, F-09 (Job Queue & Parquet), F-10, F-11 (Profiling) |

### 1.1 Objective
Membangun pipeline ingesti dataset yang tangguh dan terisolasi: upload file CSV/XLSX streaming di Go API, orkestrasi background job berbasis PostgreSQL (`SKIP LOCKED`), normalisasi data dan penulisan Parquet versi immutable di Python Engine, serta kalkulasi otomatis profil kualitas dataset menggunakan DuckDB.

### 1.2 Scope
**In Scope:**
- Upload streaming multipart di Go API dengan batas keras MVP (50 MB CSV / 20 MB XLSX), validasi ekstensi, MIME type sniffing, signature magic numbers, dan kalkulasi SHA-256 on-the-fly.
- Transaksi database atomik di Go: pembuatan metadata dataset, record versi baru (`dataset_versions`), dan antrean job pemrosesan (`processing_jobs`).
- Background job worker di Go: goroutine worker pool (default 2 workers), polling transaksi aman `SELECT ... FOR UPDATE SKIP LOCKED`, mekanisme retry eksponensial (maks 3x), penanganan kegagalan eksplisit.
- Internal IPC: Go memanggil endpoint Python Engine `POST /v1/datasets/process` membawa `dataset_ref`, `X-Internal-Token`, dan `X-Request-ID`.
- Engine Pipeline: verifikasi checksum file sumber, parsing CSV/XLSX streaming, inferensi tipe kolom ketat, konversi ke file Apache Parquet versi immutable, penulisan atomik file Parquet.
- Profiling DuckDB: scan Parquet read-only, kalkulasi statistik deskriptif per kolom (null count, distinct count, min/max/mean/median, kuartil), deteksi masalah kualitas data, penyimpanan ringkasan profil dalam format `jsonb`.
- Frontend Web: Komponen dropzone upload dataset, progress bar streaming, polling status job, dan badge status dataset (`queued`, `processing`, `ready`, `failed`).

**Out of Scope:**
- Integrasi antrean pesan eksternal terdistribusi (seperti Celery atau Redis Streams).
- Pemrosesan file format lain di luar CSV dan XLSX (misal: JSON, XML, Parquet langsung dari upload).
- Fitur transformasi atau data cleaning interaktif (fitur P2).

### 1.3 Requirements Traceability
| Requirement ID | Deskripsi Kebutuhan | Rencana Implementasi | Sumber Dokumen |
|---|---|---|---|
| F-06, F-07 | Upload file multipart streaming & validasi signature | Handler upload Go & stream SHA-256 | PRD Bagian 14 & 15 |
| F-08, F-09 | Antrean background job PostgreSQL & konversi Parquet | Worker `SKIP LOCKED` Go & Ingestion pipeline Engine | PRD Bagian 16 & 17 |
| F-10, F-11 | Profiling kualitas otomatis & deteksi tipe kolom | DuckDB profiler di Engine & penyimpanan jsonb | PRD Bagian 18 & 19 |
| SEC-UPLOAD | Sanitasi nama file, zip bomb XLSX check, no macro | Upload validator Go & OpenPyXL memory safe | Security.md Bagian 4.1 |

---

## 2. Technical Context

Alur pemrosesan dataset:
1. Browser mengunggah file multipart ke Go API: `POST /api/v1/workspaces/:workspace_id/datasets`.
2. Go API memeriksa ukuran dan signature:
   - CSV: UTF-8 check, delimiter detection.
   - XLSX: ZIP magic number (`PK\x03\x04`), sanitasi macro/formula, pemeriksaan ukuran terkompresi vs uncompressed (proteksi zip bomb).
3. File di-stream langsung ke `storage/sources/{workspace_id}/{dataset_id}/{version_no}/source.{ext}` sambil menghitung hash SHA-256 secara langsung tanpa membengkakkan RAM.
4. Dalam satu transaksi DB, Go membuat dataset, dataset_version (`status = queued`), dan processing_job (`status = queued`). HTTP 202 Accepted dikembalikan ke browser beserta ID dataset.
5. Worker background Go mengambil job berikutnya menggunakan query `FOR UPDATE SKIP LOCKED`.
6. Worker memanggil Python Engine melalui HTTP internal `POST /v1/datasets/process`.
7. Python Engine memverifikasi SHA-256 sumber, membaca data, melakukan normalisasi tipe dengan DuckDB/PyArrow, menulis file `data.parquet` secara atomik, menjalankan query statistik profil data, dan mengembalikan skema serta profil JSON ke Go API.
8. Go API memperbarui `dataset_versions` menjadi `ready`, menyimpan skema kolom ke `dataset_columns`, profil ke `dataset_profiles`, dan menandai job `completed`.

---

## 3. Implementation Tasks

### Task ID: TASK-401
**Task Name:** Multipart Streaming Upload & File Security Sanitizer
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-002, PLAN-003 |
| Related Requirements | F-06, F-07, SEC-UPLOAD |

**Objective:**
Mengimplementasikan endpoint penerimaan file upload multipart di Go API dengan streaming langsung ke disk, pembatasan ukuran ketat, dan sanitasi keamanan file.

**Implementation Steps:**
1. Di `apps/api/internal/modules/datasets`, implementasikan `upload_handler.go`.
2. Terapkan batas ukuran keras: 50 MB untuk CSV, 20 MB untuk XLSX. Tolak request yang melebihi batas sebelum buffer memori penuh (HTTP 413).
3. Lakukan inspeksi byte awal (magic numbers):
   - XLSX: verifikasi tanda tangan ZIP `PK\x03\x04`.
   - CSV: verifikasi encoding teks UTF-8 / ASCII yang sah.
4. Lakukan streaming file ke disk lokal menggunakan `io.TeeReader` yang menulis ke path `storage/sources/...` sekaligus mengkalkulasi hash SHA-256.
5. Simpan nama file asli hanya sebagai metadata yang telah disanitasi (hapus karakter path separator, kontrol, dan batasi panjang maksimal 255 karakter).

**Expected Files:**
- `apps/api/internal/modules/datasets/upload_handler.go`
- `apps/api/internal/modules/datasets/validator.go`

**Technical Requirements:**
- Dilarang memuat seluruh file ke memori (RAM) aplikasi (`MultipartReader` streaming).
- Path file fisik dibangun secara eksklusif oleh server via `StorageResolver`.

**Testing Requirements:**
- Test penolakan file palsu (contoh file executable yang dinamai `.xlsx`).
- Test penolakan file yang ukurannya melebihi kuota 50 MB.

**Acceptance Criteria:**
- File tersimpan di disk lokal, hash SHA-256 cocok, dan endpoint mengembalikan HTTP 202 dengan `dataset_id`.

---

### Task ID: TASK-402
**Task Name:** Transactional Dataset & Version Creation
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Small |
| Status | Planned |
| Dependencies | TASK-401 |
| Related Requirements | F-07, F-08 |

**Objective:**
Membuat record dataset, versi awal, dan pendaftaran job antrean pemrosesan di PostgreSQL dalam satu transaksi atomik.

**Implementation Steps:**
1. Gunakan `TxManager` database Go API.
2. Insert ke `datasets` (nama dataset, workspace_id, created_by).
3. Insert ke `dataset_versions` (dataset_id, version_no=1, file_format, checksum_sha256, status='queued').
4. Insert ke `processing_jobs` (workspace_id, version_id, job_type='dataset_ingest', status='queued', attempts=0).
5. Buat endpoint polling `GET /api/v1/datasets/:id/status` agar klien dapat memantau progres pemrosesan versi aktif.

**Expected Files:**
- `apps/api/internal/modules/datasets/service.go`
- `apps/api/internal/modules/datasets/repository.go`

**Technical Requirements:**
- Integritas data: dilarang ada file sumber yang terdaftar tanpa job pemrosesan yang sesuai.

**Testing Requirements:**
- Unit test database transaksi: simulasikan error pada insert job untuk memastikan dataset dibatalkan (rollback).

**Acceptance Criteria:**
- Record dataset dan job tersimpan di database secara atomik.

---

### Task ID: TASK-403
**Task Name:** PostgreSQL Background Job Worker Engine
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-402 |
| Related Requirements | F-08, F-09 |

**Objective:**
Membangun background worker terpisah di Go API untuk mengambil job dari antrean PostgreSQL secara aman dan mengorkestrasi pemanggilan Engine.

**Implementation Steps:**
1. Di `apps/api/internal/modules/jobs`, implementasikan worker pool dengan goroutine (default 2 workers).
2. Buat loop polling berkala (interval 1–2 detik saat ada antrean) menggunakan query:
   `SELECT id, version_id, workspace_id FROM processing_jobs WHERE status = 'queued' ORDER BY created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED`.
3. Tandai job menjadi `processing`, set waktu mulai, dan naikkan counter `attempts`.
4. Panggil client HTTP Engine internal `POST /v1/datasets/process` dengan context timeout (default 5 menit).
5. Jika Engine sukses: perbarui metadata versi menjadi `ready`, simpan skema dan profil, tandai job `completed`.
6. Jika Engine gagal: terapkan retry eksponensial (maksimal 3x percobaan untuk error transient). Jika melebihi batas, tandai versi dan job sebagai `failed` beserta pesan error tersanitasi.

**Expected Files:**
- `apps/api/internal/modules/jobs/worker.go`
- `apps/api/internal/modules/jobs/dispatcher.go`
- `apps/api/internal/platform/engineclient/client.go`

**Technical Requirements:**
- Worker harus graceful saat aplikasi Go dimatikan (context cancellation).
- Menggunakan header `X-Internal-Token` untuk autentikasi antarlayanan.

**Testing Requirements:**
- Integration test worker mengambil job menggunakan dua goroutine konkuren tanpa terjadi race condition atau klaim ganda.

**Acceptance Criteria:**
- Job berhasil diklaim secara berurutan, diproses, dan status diperbarui secara benar di PostgreSQL.

---

### Task ID: TASK-404
**Task Name:** Python Engine Dataset Ingestion & Parquet Normalizer
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Large |
| Status | Planned |
| Dependencies | PLAN-001, PLAN-002 |
| Related Requirements | F-08, F-09 |

**Objective:**
Mengimplementasikan pipeline normalisasi data di Python Engine: parsing CSV/XLSX, inferensi skema tipe data, dan konversi ke Parquet terkompresi snappy.

**Implementation Steps:**
1. Di `apps/engine/app/data_engine`, buat modul `ingest.py`.
2. Implementasikan verifikasi checksum SHA-256 dari file sumber sebelum dibaca.
3. Parsing file:
   - CSV: Gunakan PyArrow CSV reader dengan inferensi tipe otomatis dan penanganan delimiter/quote.
   - XLSX: Gunakan openpyxl/calamine dengan batasan memori (read-only mode), streaming sheet pertama, konversi ke tabel Arrow perantara.
4. Normalisasi nama kolom: bersihkan whitespace, ganti karakter non-alfanumerik dengan underscore, jadikan lowercase, tangani nama kolom duplikat.
5. Tulis tabel Apache Parquet ke path `storage/parquet/.../data.parquet` secara atomik (tulis ke file `.tmp` lalu rename).
6. Kembalikan daftar metadata kolom (nama asli, nama normalisasi, tipe data yang diinferensikan, jumlah baris).

**Expected Files:**
- `apps/engine/app/api/v1/datasets.py`
- `apps/engine/app/data_engine/ingest.py`
- `apps/engine/app/data_engine/normalizer.py`

**Technical Requirements:**
- DuckDB/PyArrow membaca Parquet dengan kompresi Snappy standar.
- File Parquet bersifat immutable per versi dataset; dilarang menimpa file yang sudah berstatus `ready`.

**Testing Requirements:**
- Unit test ingest berbagai variasi CSV (koma, titik koma, spasi, header bermasalah) dan XLSX.

**Acceptance Criteria:**
- File Parquet terbit di direktori storage lokal dan dapat dibaca oleh DuckDB tanpa error.

---

### Task ID: TASK-405
**Task Name:** Automatic Dataset Profiling with DuckDB
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-404 |
| Related Requirements | F-10, F-11 |

**Objective:**
Membangun modul kalkulasi profil statistik dataset secara otomatis menggunakan query analitik agregasi DuckDB terhadap file Parquet.

**Implementation Steps:**
1. Di `apps/engine/app/profiling`, buat modul `profiler.py`.
2. Buat koneksi read-only DuckDB in-memory yang mengarah ke file Parquet target.
3. Jalankan query agregasi untuk setiap kolom:
   - Kolom Numerik: `COUNT(*)`, `COUNT(DISTINCT)`, `COUNT(NULL)`, `MIN`, `MAX`, `AVG`, `STDDEV`, persentil (25, 50, 75).
   - Kolom Teks/Kategorikal: `COUNT(*)`, `COUNT(DISTINCT)`, `COUNT(NULL)`, top 5 nilai terbanyak, min length, max length.
   - Kolom Tanggal/Waktu: min date, max date, null count.
4. Kalkulasi skor kualitas data (0–100) berdasarkan rasio kelengkapan nilai, duplikasi baris, dan anomali tipe.
5. Kembalikan payload profil JSON terstruktur sesuai skema Pydantic `DatasetProfile`.

**Expected Files:**
- `apps/engine/app/profiling/profiler.py`
- `apps/engine/app/schemas/profile.py`

**Technical Requirements:**
- DuckDB dibatasi maksimal memori 1 GB dan 2 thread untuk mencegah lonjakan CPU mesin lokal.
- Timeout eksekusi profil maksimal 30 detik.

**Testing Requirements:**
- Uji kalkulasi profil pada dataset sampel dengan data null dan outlier numerik.

**Acceptance Criteria:**
- Profil statistik lengkap terbentuk dalam format JSON dan tersimpan di `dataset_profiles` PostgreSQL.

---

## 4. API & Database Changes

### API Changes
| Method | Endpoint | Purpose | Authentication |
|---|---|---|---|
| POST | `/api/v1/workspaces/:workspace_id/datasets` | Upload file CSV/XLSX (multipart) | Bearer JWT + Member |
| GET | `/api/v1/datasets/:id` | Mengambil detail metadata & profil dataset | Bearer JWT + Member |
| GET | `/api/v1/datasets/:id/status` | Polling status pemrosesan dataset | Bearer JWT + Member |
| POST | `/v1/datasets/process` (Engine Internal) | Eksekusi normalisasi & profil Parquet | `X-Internal-Token` |

### Database Changes
Data diperbarui pada tabel yang ada:
- `datasets`: diperbarui `current_version_no`.
- `dataset_versions`: `row_count`, `byte_size`, `checksum_sha256`, `status` ('processing', 'ready', 'failed').
- `dataset_columns`: daftar kolom yang dihasilkan oleh normalizer.
- `dataset_profiles`: JSON profil statistik lengkap disimpan pada kolom `profile_json`.
- `processing_jobs`: `status`, `attempts`, `error_message`, `updated_at`.

---

## 5. Design & UI Implementation

- **Related Pages:**
  - `/app/:workspaceId/datasets`: Daftar dataset dengan tombol "Unggah Dataset".
  - `/app/:workspaceId/datasets/new`: Dialog modal unggah file atau halaman upload drag-and-drop.
  - `/app/:workspaceId/datasets/:id`: Halaman detail dataset menampilkan kartu metrik profil kualitas.
- **Component Requirements:**
  - `Dropzone`: area drag-and-drop file dengan indikator tipe format `.csv` dan `.xlsx`.
  - `UploadProgressBar`: indikator progres streaming HTTP ke server.
  - `DatasetStatusBadge`: badge warna status (`queued` = abu-abu, `processing` = biru berputar, `ready` = hijau, `failed` = merah).
  - `QualityScoreCard`: visualisasi skor kualitas data (0-100) dengan indikator persentase null dan duplikat.
- **Design Tokens:**
  - Status tokens: `--success` (#15803D), `--warning` (#B45309), `--danger` (#B91C1C), `--info` (#0369A1).

---

## 6. Security Requirements

- Validasi Magic Bytes: Menolak file upload yang hanya mengganti ekstensi file executable menjadi `.xlsx`.
- Proteksi Zip Bomb: Memeriksa header ZIP XLSX untuk rasio kompresi tidak wajar (>100:1) dan batasan uncompressed size maksimal 200 MB.
- Path Isolasi: File sumber dan Parquet disimpan menggunakan struktur folder server yang ketat; nama file asli pengguna tidak pernah digunakan sebagai path filesystem.
- Engine Internal Guard: Endpoint `/v1/datasets/process` menolak request tanpa header `X-Internal-Token` yang cocok dengan konfigurasi lingkungan.

---

## 7. Testing & Verification

- **Unit Tests:**
  - Sanitizer nama file Go API.
  - Normalizer tipe kolom PyArrow di Python Engine.
  - Profiler DuckDB untuk ringkasan agregasi numerik.
- **Integration Tests:**
  - Skenario upload file CSV valid -> worker memproses -> versi dataset berstatus `ready` dengan profil terisi.
  - Skenario penolakan file CSV rusak / non-UTF8.
- **Verification Commands:**
  - `cd apps/api && go test -v ./internal/modules/datasets/...`
  - `cd apps/engine && pytest tests/test_ingest.py tests/test_profiler.py`

---

## 8. Commit Plan

### 8.1 Commit Strategy
Memisahkan handler upload streaming, antrean worker Go, engine ingest Parquet, modul profiling DuckDB, dan komponen frontend.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-401 | feat | datasets | feat(datasets): implement multipart streaming upload and file signature validation |
| 02 | TASK-402 | feat | datasets | feat(datasets): add transactional dataset and version initialization |
| 03 | TASK-403 | feat | jobs | feat(jobs): implement PostgreSQL background worker with SKIP LOCKED queue |
| 04 | TASK-404 | feat | engine | feat(engine): implement CSV and XLSX ingestion to immutable Parquet |
| 05 | TASK-405 | feat | engine | feat(engine): implement automatic statistical profiling using DuckDB |
| 06 | TASK-401..405 | feat | web | feat(web): add dataset upload dropzone, status polling, and profile summary UI |

### 8.3 Commit Details
- **Commit 01:** Menyertakan `upload_handler.go` dan validator magic bytes.
- **Commit 02:** Menyertakan repositori transaksi dataset dan versi.
- **Commit 03:** Menyertakan dispatcher job worker dan client IPC HTTP ke Engine.
- **Commit 04:** Menyertakan modul ingest Arrow/Parquet di FastAPI Engine.
- **Commit 05:** Menyertakan query agregasi DuckDB dan skema Pydantic profil dataset.
- **Commit 06:** Menyertakan komponen React UI dropzone, status polling hook, dan badge kualitas dataset.

### 8.4 Commit Boundaries
- Pipeline file mentah dan normalisasi Parquet backend harus terverifikasi sebelum komponen upload frontend diintegrasikan.

### 8.5 Commit Dependencies
- Commit 02 bergantung pada 01. Commit 03 bergantung pada 02. Commit 04 dan 05 dieksekusi di engine. Commit 06 bergantung pada integrasi API.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Parsing file XLSX berukuran besar menguras RAM Python Engine | OOM crash pada Uvicorn | Sedang | Batasi batas upload XLSX 20 MB; gunakan streaming parsing OpenPyXL/Calamine read-only tanpa memuat formula tree. |
| CSV dengan delimiter tidak lazim (tab, pipa) | Gagal inferensi kolom | Sedang | Lakukan deteksi delimiter otomatis (sniffing) pada 1.000 baris pertama sebelum konversi. |

---

## 10. Definition of Done

- [ ] Upload file CSV dan XLSX streaming berfungsi hingga batas ukuran tanpa OOM.
- [ ] Worker PostgreSQL `SKIP LOCKED` mengklaim dan memproses job secara idempoten.
- [ ] File Parquet versi immutable berhasil dibuat di folder `storage/parquet/`.
- [ ] Profil statistik kualitas dataset terhitung dan tersimpan di database.
- [ ] Status pemrosesan dataset terpantau secara real-time di UI frontend.
- [ ] Seluruh unit dan integration test pipeline ingest lulus.
