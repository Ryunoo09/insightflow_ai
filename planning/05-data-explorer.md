# Plan 05: Data Explorer & Virtualized Table Interface

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-005 |
| Name | Data Explorer, Server-Side Pagination & Virtualized Grid |
| Priority | P1 – High |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-004 |
| Related Requirements | F-12, F-13 (Data Explorer & AG Grid), UX-EXPLORER, SEC-ACCESS |

### 1.1 Objective
Membangun antarmuka eksplorasi data mentah interaktif yang mampu merender ratusan ribu baris dataset secara efisien menggunakan model virtualisasi AG Grid Community Infinite Row Model, didukung oleh query paginasi, penyortiran, dan penyaringan server-side terparameter di DuckDB.

### 1.2 Scope
**In Scope:**
- Endpoint Go API `POST /api/v1/datasets/:id/rows/query` untuk melayani request paginasi baris tabel dari frontend.
- Endpoint Engine internal `POST /v1/datasets/rows/query` yang mengeksekusi query `SELECT` terparameter dengan `LIMIT`, `OFFSET`, `ORDER BY`, dan filter kolom allowlist pada file Parquet read-only menggunakan DuckDB.
- Proteksi injeksi SQL pada penyortiran dan penyaringan: nama kolom divalidasi terhadap daftar kolom resmi di `dataset_columns`, operator dibatasi pada allowlist (`eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `contains`).
- Komponen frontend React Data Explorer menggunakan AG Grid Community:
  - Infinite Row Model (blok per 100 baris, cache maksimal 10 blok).
  - Header kolom sticky, quick filter pencarian, dan indikator tipe data kolom (numerik, teks, tanggal).
  - Paginasi URL synchronizer (menyimpan state filter dan sort pada query parameter URL agar shareable).
  - Switcher versi dataset untuk memeriksa snapshot data historis jika dataset memiliki lebih dari satu versi.

**Out of Scope:**
- Fitur inline editing sel data (dataset bersifat read-only immutable pada MVP).
- Fitur ekspor CSV dari explorer (fitur P2 per PRD F-28).
- AG Grid Enterprise features (pivot table, grouping berjenjang, chart bawaan AG Grid).

### 1.3 Requirements Traceability
| Requirement ID | Deskripsi Kebutuhan | Rencana Implementasi | Sumber Dokumen |
|---|---|---|---|
| F-12 | Eksplorasi data tabular dengan filter dan sort server-side | Query DuckDB paginasi terparameter & handler Go | PRD Bagian 20 |
| F-13 | Virtualized table frontend dengan AG Grid Community | Komponen `DataTable` AG Grid Infinite Row Model | PRD Bagian 21 |
| UX-EXPLORER | Layout full-width, format angka tabular, sticky header | Halaman Explorer & styling token Design.md | Design.md Bagian 5.2 |
| SEC-PARAM-SQL | Larangan SQL dinamis dari input pengguna bebas | Allowlist identifier & parameter binding DuckDB | Security.md Bagian 4.2 |

---

## 2. Technical Context

Alur pemanggilan Data Explorer:
1. Pengguna membuka tab "Data Explorer" pada halaman dataset: `/app/:workspaceId/datasets/:id/explorer`.
2. AG Grid menginisialisasi `IDatasource` dengan `getBlock(params)` (ukuran blok default 100 baris).
3. Frontend mengirim request POST ke Go API:
   ```json
   {
     "offset": 0,
     "limit": 100,
     "sort": [{ "column": "revenue", "direction": "desc" }],
     "filters": [{ "column": "category", "operator": "eq", "value": "Electronics" }]
   }
   ```
4. Go API memverifikasi otorisasi keanggotaan workspace dan memastikan versi dataset berstatus `ready`.
5. Go API meneruskan request ke Python Engine internal via HTTP dengan header `X-Internal-Token`.
6. Engine internal memvalidasi seluruh nama kolom terhadap skema Parquet. Query compiler internal menyusun query DuckDB parameterized:
   `SELECT col1, col2, col3 FROM read_parquet(?) WHERE category = ? ORDER BY revenue DESC LIMIT ? OFFSET ?`.
7. Hasil dibaca sebagai baris JSON dan dikembalikan ke Go API -> browser untuk dirender virtual oleh AG Grid.

---

## 3. Implementation Tasks

### Task ID: TASK-501
**Task Name:** Parameterized Explorer Query Compiler & DuckDB Pagination Engine
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-004 |
| Related Requirements | F-12, SEC-PARAM-SQL |

**Objective:**
Mengimplementasikan endpoint internal di Python Engine untuk mengeksekusi paginasi dan filter dataset terhadap file Parquet secara aman menggunakan DuckDB.

**Implementation Steps:**
1. Di `apps/engine/app/api/v1/explorer.py`, buat endpoint `POST /v1/datasets/rows/query`.
2. Buat skema Pydantic `ExplorerQueryRequest` (offset, limit maksimal 500, sort, filters).
3. Buat modul compiler query `apps/engine/app/analytics/explorer_compiler.py`:
   - Validasi bahwa setiap kolom pada `sort` dan `filters` ada dalam daftar kolom skema file Parquet. Tolak jika ada kolom tak dikenal.
   - Izinkan operator filter: `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `contains` (case-insensitive substring).
   - Susun prepared statement DuckDB dengan binding nilai menggunakan placeholder `?`.
4. Eksekusi query dengan connection read-only DuckDB, sertakan timeout eksekusi 5 detik.
5. Jalankan query hitung total baris yang cocok (`SELECT COUNT(*) FROM read_parquet(?) WHERE ...`) untuk informasi paginasi.
6. Kembalikan array baris data dan total count.

**Expected Files:**
- `apps/engine/app/api/v1/explorer.py`
- `apps/engine/app/analytics/explorer_compiler.py`
- `apps/engine/app/schemas/explorer.py`

**Technical Requirements:**
- Tidak ada identifier kolom atau string filter yang di-concatenate langsung ke teks query SQL.
- Limit query dipatok maksimal 500 baris per request.

**Testing Requirements:**
- Unit test compiler dengan upaya SQL injection pada nama kolom (contoh: `col; DROP TABLE...`) untuk memastikan penolakan error.

**Acceptance Criteria:**
- Endpoint mengembalikan subset baris data yang tepat sesuai limit, offset, dan filter yang diberikan.

---

### Task ID: TASK-502
**Task Name:** Go API Explorer Gateway & Cache Coordination
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Small |
| Status | Planned |
| Dependencies | TASK-501 |
| Related Requirements | F-12, SEC-ACCESS |

**Objective:**
Membangun endpoint proxy terlindungi di Go API untuk validasi otorisasi dan penyerahan request paginasi ke Python Engine.

**Implementation Steps:**
1. Di `apps/api/internal/modules/datasets`, buat `explorer_handler.go`.
2. Terapkan route `POST /api/v1/datasets/:id/rows/query`:
   - Bind request JSON dan validasi batas `limit` (default 100, max 200).
   - Verifikasi keanggotaan workspace dan pastikan dataset versi aktif berstatus `ready`.
   - Panggil Engine internal menggunakan `EngineClient`.
   - Teruskan response JSON ke browser.

**Expected Files:**
- `apps/api/internal/modules/datasets/explorer_handler.go`

**Technical Requirements:**
- Memastikan `X-Request-ID` diteruskan ke Engine untuk pelacakan performa.

**Testing Requirements:**
- Integration test akses explorer dengan token pengguna yang tidak memiliki akses ke workspace (respons 404).

**Acceptance Criteria:**
- Klien yang terautentikasi dapat meminta baris dataset dan menerima payload data tabel dengan cepat (<300ms untuk dataset standar).

---

### Task ID: TASK-503
**Task Name:** React AG Grid Virtualized Explorer Component
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-502 |
| Related Requirements | F-13, UX-EXPLORER |

**Objective:**
Membangun komponen frontend Data Explorer menggunakan AG Grid Community Infinite Row Model dengan layout full-width yang responsif.

**Implementation Steps:**
1. Di `apps/web/src/features/datasets/components`, buat `DataExplorer.tsx`.
2. Konfigurasi AG Grid Community dengan tema custom yang memetakan CSS variables Tailwind (`--background`, `--surface`, `--border`, `--foreground`).
3. Implementasikan `datasource`:
   - Memanggil `apiClient.queryRows(datasetId, params)`.
   - Menyediakan callback `successCallback(rows, totalRows)` dan `failCallback()`.
4. Tambahkan header kontrol:
   - Quick search input.
   - Dropdown pemilih versi dataset (menampilkan versi aktif dan opsi melihat versi lama).
   - Badge jumlah total baris dan total kolom.
5. Pastikan kolom angka menggunakan perataan kanan (`text-right`) dan `font-variant-numeric: tabular-nums` sesuai `Design.md`.
6. Simpan state filter dan sorting ke URL search params menggunakan React Router.

**Expected Files:**
- `apps/web/src/features/datasets/components/DataExplorer.tsx`
- `apps/web/src/features/datasets/hooks/useDataExplorer.ts`
- `apps/web/src/features/datasets/api/explorer.api.ts`

**Technical Requirements:**
- Layout halaman explorer diizinkan menggunakan lebar penuh (full-width) tanpa batasan 1280px agar tabel terbaca.
- Penanganan error state jika koneksi gagal atau query timeout.

**Testing Requirements:**
- Component test rendering AG Grid dengan mock data 100 baris.

**Acceptance Criteria:**
- Pengguna dapat menggulir data ratusan baris secara lancar tanpa lag render, melakukan sorting kolom, dan memfilter nilai.

---

## 4. API & Database Changes

### API Changes
| Method | Endpoint | Purpose | Authentication |
|---|---|---|---|
| POST | `/api/v1/datasets/:id/rows/query` | Paginasi, sort, dan filter data explorer | Bearer JWT + Member |
| POST | `/v1/datasets/rows/query` (Engine Internal) | Eksekusi query Parquet di DuckDB | `X-Internal-Token` |

### Database Changes
`Not Applicable` (Membaca langsung dari file Parquet di storage layer).

---

## 5. Design & UI Implementation

- **Related Page:** `/app/:workspaceId/datasets/:id/explorer`.
- **Layout Requirements:** Mode layout full-width (lebar layar penuh di dalam AppShell) sesuai `Design.md` Bagian 5.2.
- **Component Requirements:**
  - AG Grid Community virtual scroll container dengan tinggi `calc(100vh - 200px)`.
  - Sticky header tabel dengan indikator arah sort (panah naik/turun).
  - Indikator tipe kolom di header (ikon pagar `#` untuk angka, teks `Aa` untuk string, kalender untuk tanggal).
- **Responsive Behavior:** Pada viewport mobile (<768px), tampilkan tabel dengan horizontal scroll yang halus dan sembunyikan quick filter kompleks.
- **Accessibility Requirements:** Navigasi sel tabel dapat diakses menggunakan keyboard (Arrow keys, Page Up, Page Down).

---

## 6. Security Requirements

- Validasi Ketat Identifier: Nama kolom pada query sort/filter dicocokkan terhadap metadata resmi `dataset_columns` di database.
- Sanitasi Nilai Filter: String nilai filter diparameterisasi secara penuh pada statement DuckDB; dilarang menggunakan format string interpolation (`f"SELECT ... WHERE col = '{val}'"`).
- Otorisasi Workspace: Setiap request explorer memverifikasi hak akses pengguna ke workspace sebelum query DuckDB dijalankan.

---

## 7. Testing & Verification

- **Unit Tests:**
  - Compiler query DuckDB di Python Engine.
  - Sanitasi argumen sort & filter.
- **Integration Tests:**
  - Request paginasi baris 0–100 pada dataset terdaftar menghasilkan 100 baris data.
  - Request sorting ASC dan DESC pada kolom tanggal/angka.
- **Verification Commands:**
  - `cd apps/engine && pytest tests/test_explorer_compiler.py`
  - `cd apps/api && go test -v ./internal/modules/datasets/...`
  - `cd apps/web && npm test`

---

## 8. Commit Plan

### 8.1 Commit Strategy
Memisahkan backend compiler query DuckDB -> handler Go API -> komponen AG Grid frontend.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-501 | feat | engine | feat(engine): implement parameterized DuckDB pagination and filtering compiler |
| 02 | TASK-502 | feat | datasets | feat(datasets): add explorer query gateway endpoint with workspace authorization |
| 03 | TASK-503 | feat | web | feat(web): implement virtualized Data Explorer table using AG Grid Infinite Model |

### 8.3 Commit Details
- **Commit 01:** Menyertakan endpoint internal `/v1/datasets/rows/query` dan modul compiler `explorer_compiler.py`.
- **Commit 02:** Menyertakan `explorer_handler.go` dan integrasi client engine.
- **Commit 03:** Menyertakan komponen React `DataExplorer.tsx` dengan konfigurasi tema CSS variables dan datasource virtual.

### 8.4 Commit Boundaries
- Endpoint query backend harus siap dan teruji sebelum komponen UI AG Grid dihubungkan.

### 8.5 Commit Dependencies
- Commit 02 bergantung pada 01. Commit 03 bergantung pada 02.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Query filter string `contains` pada kolom teks berukuran sangat besar lambat | Latensi tinggi pada DuckDB | Sedang | Pasang timeout query 5 detik dan batasi pembacaan baris dengan LIMIT maksimal 200 di dev. |
| Ukuran bundle AG Grid Community memperlambat initial page load | LCP frontend menurun | Rendah | Gunakan dynamic `React.lazy()` import untuk modul Data Explorer. |

---

## 10. Definition of Done

- [ ] Endpoint paginasi DuckDB mengembalikan baris data yang tepat dengan filter parameterized.
- [ ] Otorisasi workspace memblokir akses ke dataset di luar workspace pengguna.
- [ ] AG Grid merender data secara virtual tanpa lag performa.
- [ ] Sorting dan filtering kolom berfungsi dan tersinkronisasi ke URL params.
- [ ] Seluruh unit dan integration test explorer lulus 100%.
