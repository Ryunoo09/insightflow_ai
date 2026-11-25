# Plan 02: Database & Infrastructure Layer

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-002 |
| Name | Database Schema, Migrations, Cache & Storage Infrastructure |
| Priority | P0 – Critical |
| Complexity | Large |
| Status | Planned |
| Depends On | PLAN-001 |
| Related Requirements | ARCH-DB (PostgreSQL 16 & sqlc), ARCH-REDIS (Redis Ephemeral/Cache), ARCH-STORAGE (Local Parquet & Source) |

### 1.1 Objective
Membangun lapisan persistensi metadata transaksional di PostgreSQL 16 menggunakan migrasi SQL berversi dan generator kode `sqlc`, klien Redis dengan pemisahan namespace/TTL, serta komponen storage resolver lokal yang aman untuk mengisolasi file dataset mentah dan Parquet per workspace.

### 1.2 Scope
**In Scope:**
- Penyusunan skrip migrasi SQL berversi (`golang-migrate`) untuk seluruh entitas metadata MVP: `users`, `refresh_tokens`, `user_identities`, `workspaces`, `workspace_members`, `datasets`, `dataset_versions`, `dataset_columns`, `dataset_profiles`, `processing_jobs`, `analysis_sessions`, `analysis_messages`, `analysis_runs`, `analysis_results`, `dashboards`, `dashboard_widgets`, `usage_records`, `audit_logs`.
- Konfigurasi `sqlc` di Go API (`apps/api/db/queries`) untuk menghasilkan kode data access layer yang type-safe.
- Implementasi `TxManager` di Go API untuk orkestrasi transaksi DB lintas repository.
- Implementasi adapter Redis di Go API dan Python Engine dengan namespace terpisah (`auth:*`, `rate:*`, `lock:*`, `rc:*`) dan kebijakan TTL mutlak.
- Implementasi Storage Resolver lokal dengan validasi path traversal dan pembuatan direktori terisolasi berbasis `dataset_ref`.

**Out of Scope:**
- Penyimpanan isi dataset mentah atau Parquet ke dalam tabel PostgreSQL.
- Setup database sharding, partitioning, atau read-replica.
- Integrasi AWS S3 / MinIO object storage (disiapkan untuk fase mendatang).

### 1.3 Requirements Traceability
| Requirement ID | Deskripsi Kebutuhan | Rencana Implementasi | Sumber Dokumen |
|---|---|---|---|
| ARCH-POSTGRES | PostgreSQL 16 sebagai metadata store transaksional tunggal | SQL migrations dan sqlc generated repositories | Architecture.md Bagian 4.3 |
| ARCH-REDIS-CACHE | Redis untuk state efemeral, OTP, rate limit, dan cache | Adapter Redis Go & Python dengan namespace | Architecture.md Bagian 2.3 |
| ARCH-LOCAL-STORAGE | Local filesystem storage untuk file sumber dan Parquet | Server-side Storage Resolver aman | Architecture.md Bagian 1.4 |
| SEC-DB-ISOLATION | Query resource wajib menyertakan otorisasi membership | Pola query parameterized sqlc dengan JOIN workspace_members | Security.md Bagian 3.3 |

---

## 2. Technical Context

PostgreSQL 16 dikelola secara eksklusif oleh Go API menggunakan pool koneksi `pgx v5`. Python Engine dilarang keras mengakses PostgreSQL langsung. Seluruh entitas analitik (tabel Parquet) disimpan pada filesystem lokal di folder `storage/`, dipindai oleh DuckDB secara read-only. Redis digunakan secara bersama oleh Go (untuk OTP, session lock, rate limit, quota counter) dan Engine (untuk cache deterministik `ResultEnvelope`) dengan prefix namespace yang terpisah.

---

## 3. Implementation Tasks

### Task ID: TASK-201
**Task Name:** PostgreSQL Schema Migrations Setup & DDL Definition
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Large |
| Status | Planned |
| Dependencies | None |
| Related Requirements | ARCH-POSTGRES, SEC-DB-ISOLATION |

**Objective:**
Menulis file migrasi up/down DDL lengkap untuk 18 entitas inti metadata MVP dengan constraint integritas referensial dan indeks terukur.

**Implementation Steps:**
1. Di `apps/api/db/migrations`, buat file migrasi awal `000001_init_schema.up.sql` dan `.down.sql`.
2. Definisikan tabel identitas: `users`, `refresh_tokens`, `user_identities`.
3. Definisikan tabel workspace: `workspaces`, `workspace_members`.
4. Definisikan tabel dataset: `datasets`, `dataset_versions`, `dataset_columns`, `dataset_profiles`.
5. Definisikan tabel job: `processing_jobs` (dengan indeks status `queued` untuk `SKIP LOCKED`).
6. Definisikan tabel analisis & dashboard: `analysis_sessions`, `analysis_messages`, `analysis_runs`, `analysis_results`, `dashboards`, `dashboard_widgets`.
7. Definisikan tabel governance: `usage_records`, `audit_logs`.
8. Tambahkan Foreign Key constraints dengan `ON DELETE CASCADE` atau `RESTRICT` yang tepat serta indeks komposit untuk lookup berbasis workspace.

**Expected Files:**
- `apps/api/db/migrations/000001_init_schema.up.sql`
- `apps/api/db/migrations/000001_init_schema.down.sql`

**Technical Requirements:**
- Semua identifier menggunakan format `snake_case` dan nama tabel jamak.
- Tipe data UUID v4 untuk seluruh primary key.
- Tipe `jsonb` untuk kolom profil, plan, spec visualisasi, dan report.

**Testing Requirements:**
- Jalankan migrasi `up` diikuti `down` lalu `up` kembali menggunakan CLI `golang-migrate` pada database test PostgreSQL lokal untuk memastikan DDL idempoten dan bersih.

**Acceptance Criteria:**
- Seluruh 18 tabel dan indeks terbentuk dengan benar pada PostgreSQL 16 tanpa syntax error.

---

### Task ID: TASK-202
**Task Name:** `sqlc` Configuration & Base Database Queries
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-201 |
| Related Requirements | ARCH-POSTGRES, SEC-DB-ISOLATION |

**Objective:**
Mengonfigurasi `sqlc.yaml` dan menulis query SQL parameterized untuk query umum agar kode Go yang dihasilkan type-safe.

**Implementation Steps:**
1. Buat file `apps/api/sqlc.yaml` dengan konfigurasi generator untuk package `dbgen` menggunakan driver `pgx/v5`.
2. Buat folder `apps/api/db/queries/` dan susun query dasar:
   - `users.sql`: GetByEmail, CreateUser, UpdateUser.
   - `workspaces.sql`: CreateWorkspace, GetWorkspaceWithMember, ListUserWorkspaces.
   - `datasets.sql`: CreateDataset, CreateDatasetVersion, GetDatasetWithAccess.
   - `jobs.sql`: EnqueueJob, ClaimNextJob (`FOR UPDATE SKIP LOCKED`), UpdateJobStatus.
3. Jalankan `sqlc generate` untuk menghasilkan kode Go di `apps/api/internal/platform/dbgen`.
4. Implementasikan `TxManager` di `apps/api/internal/platform/database` untuk mendukung transaksi lintas repository.

**Expected Files:**
- `apps/api/sqlc.yaml`
- `apps/api/db/queries/users.sql`
- `apps/api/db/queries/workspaces.sql`
- `apps/api/db/queries/datasets.sql`
- `apps/api/db/queries/jobs.sql`
- `apps/api/internal/platform/database/tx.go`

**Technical Requirements:**
- Seluruh query resource wajib menyertakan verifikasi membership dalam query yang sama.
- Dilarang keras mengedit kode hasil generate `dbgen` secara manual.

**Testing Requirements:**
- Unit test Go yang memverifikasi transaksi atomik `TxManager` commit dan rollback.

**Acceptance Criteria:**
- `sqlc generate` berjalan sukses tanpa error dan menghasilkan struct type-safe.

---

### Task ID: TASK-203
**Task Name:** Redis Connection Pool & Ephemeral Namespace Adapter
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | None |
| Related Requirements | ARCH-REDIS-CACHE |

**Objective:**
Membangun adapter Redis terstruktur di Go API dan Python Engine dengan konvensi key, penanganan timeout, dan kebijakan TTL wajib.

**Implementation Steps:**
1. Di Go API (`apps/api/internal/platform/cache`), inisialisasi client `go-redis` dengan konfigurasi connection pool (max 10 koneksi pada dev).
2. Terapkan helper operasi ber-TTL: `SetWithTTL`, `Get`, `Delete`, `IncrementWithTTL`, `AcquireLock`.
3. Definisikan namespace constants: `otp:{challenge_id}`, `oauth:{state}`, `rate:{ip_route}`, `lock:{resource_id}`.
4. Di Python Engine (`apps/engine/app/cache`), buat client Redis async dengan `redis-py` khusus untuk cache hasil deterministik `rc:{tool_hash}` dengan TTL 24 jam.

**Expected Files:**
- `apps/api/internal/platform/cache/redis.go`
- `apps/engine/app/cache/redis.py`

**Technical Requirements:**
- Semua key Redis wajib memiliki TTL eksplisit (tidak ada persistent key).
- Kebijakan Fail-Closed untuk autentikasi dan Fail-Open untuk read-cache.

**Testing Requirements:**
- Integration test pada Go dan Python yang melakukan set dan get key dengan verifikasi TTL.

**Acceptance Criteria:**
- Operasi Redis sukses terhubung ke instance Memurai/Redis lokal dan key kadaluarsa sesuai TTL yang ditetapkan.

---

### Task ID: TASK-204
**Task Name:** Server-Side Storage Resolver & Path Sanitization
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Small |
| Status | Planned |
| Dependencies | None |
| Related Requirements | ARCH-LOCAL-STORAGE |

**Objective:**
Mengimplementasikan modul resolver path penyimpanan lokal yang aman untuk mengisolasi file upload mentah dan file Parquet berdasarkan `dataset_ref`.

**Implementation Steps:**
1. Di Go API (`apps/api/internal/platform/storage`), buat interface `StorageResolver`.
2. Implementasikan pembuatan path:
   - Source upload: `storage/sources/{workspace_id}/{dataset_id}/{version_no}/source.{ext}`
   - Parquet table: `storage/parquet/{workspace_id}/{dataset_id}/{version_no}/data.parquet`
   - Temporary file: `storage/temp/{uuid}.tmp`
3. Tambahkan validasi path traversal (menolak path yang mengandung `..` atau separator absolut).
4. Port logika path resolver yang identik ke Python Engine (`apps/engine/app/storage/resolver.py`).

**Expected Files:**
- `apps/api/internal/platform/storage/resolver.go`
- `apps/engine/app/storage/resolver.py`

**Technical Requirements:**
- Caller dari API dilarang menentukan path absolut filesystem.
- Folder direktori parent dibuat secara otomatis dengan izin terbatas.

**Testing Requirements:**
- Unit test dengan payload path traversal jahat (contoh: `../../etc/passwd`) untuk memastikan penolakan error.

**Acceptance Criteria:**
- Resolver membangun path yang valid dan aman di dalam subfolder `storage/` dan menolak traversal attempt.

---

## 4. API & Database Changes

### API Changes
`Not Applicable` (Komponen infrastruktur internal).

### Database Changes
18 Tabel baru di PostgreSQL 16:
- `users`: ID, email, password_hash (Argon2id), full_name, status, created_at, updated_at.
- `refresh_tokens`: ID, user_id, token_hash, family_id, expires_at, revoked_at, created_at.
- `user_identities`: ID, user_id, provider (google), provider_subject, created_at.
- `workspaces`: ID, name, slug, created_by, created_at, updated_at.
- `workspace_members`: ID, workspace_id, user_id, role (owner), joined_at.
- `datasets`: ID, workspace_id, name, description, current_version_no, created_at, updated_at.
- `dataset_versions`: ID, dataset_id, version_no, status, file_format, row_count, byte_size, checksum_sha256, created_at.
- `dataset_columns`: ID, version_id, column_name, original_name, inferred_type, is_nullable, column_order.
- `dataset_profiles`: ID, version_id, profile_json (jsonb), quality_score, created_at.
- `processing_jobs`: ID, workspace_id, version_id, job_type, status, attempts, error_message, created_at, updated_at.
- `analysis_sessions`: ID, workspace_id, dataset_id, title, created_by, created_at, updated_at.
- `analysis_messages`: ID, session_id, role, content, client_message_id, created_at.
- `analysis_runs`: ID, session_id, message_id, status, error_code, prompt_tokens, completion_tokens, created_at.
- `analysis_results`: ID, run_id, tool_name, result_json (jsonb), chart_spec_json (jsonb), created_at.
- `dashboards`: ID, workspace_id, title, description, layout_json (jsonb), created_at, updated_at.
- `dashboard_widgets`: ID, dashboard_id, title, widget_type, query_spec_json (jsonb), chart_spec_json (jsonb), created_at.
- `usage_records`: ID, workspace_id, metric_type, amount, recorded_at.
- `audit_logs`: ID, workspace_id, user_id, action, resource_type, resource_id, details (jsonb), ip_address, created_at.

---

## 5. Design & UI Implementation
`Not Applicable`

---

## 6. Security Requirements

- Database credentials dikonfigurasi melalui environment variable `DATABASE_URL` bertipe, dilarang hardcoded.
- Query sqlc selalu menggunakan parameterized statement (`$1, $2`) untuk mencegah SQL Injection.
- Storage resolver mencegah directory traversal attack secara deterministik.
- Redis client tidak menyimpan raw password atau data plaintext sensitif.

---

## 7. Testing & Verification

- **Integration Tests:**
  - Migrasi skema berjalan lancar pada instance PostgreSQL lokal.
  - Test transaksi database membuktikan atomicity pembaruan status job.
  - Test Redis TTL memverifikasi hilangnya key setelah waktu kedaluwarsa.
  - Test Storage Resolver menolak karakter illegal dan traversal pattern.
- **Verification Commands:**
  - `cd apps/api && go test -v ./internal/platform/database/...`
  - `cd apps/api && go test -v ./internal/platform/storage/...`
  - `cd apps/api && go test -v ./internal/platform/cache/...`
  - `cd apps/engine && pytest tests/test_storage.py`

---

## 8. Commit Plan

### 8.1 Commit Strategy
Memisahkan migrasi DDL, konfigurasi sqlc, adapter Redis, dan storage resolver ke dalam commit individual yang terverifikasi.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-201 | feat | db | feat(db): create initial PostgreSQL DDL migrations for all core entities |
| 02 | TASK-202 | feat | api | feat(api): configure sqlc and implement transactional database manager |
| 03 | TASK-203 | feat | platform | feat(platform): implement Redis clients with strict namespace and TTL policies |
| 04 | TASK-204 | feat | storage | feat(storage): implement server-side path resolver with traversal protection |

### 8.3 Commit Details
- **Commit 01:** Menyertakan file migrasi DDL `000001_init_schema.up.sql` dan `000001_init_schema.down.sql`.
- **Commit 02:** Menyertakan konfigurasi `sqlc.yaml`, query SQL mentah di `db/queries`, kode `dbgen`, dan `tx.go`.
- **Commit 03:** Menyertakan adapter Redis untuk Go (`apps/api`) dan Python (`apps/engine`).
- **Commit 04:** Menyertakan modul `StorageResolver` pada kedua runtime Go dan Python beserta unit test-nya.

### 8.4 Commit Boundaries
- Migrasi database harus berada sebelum query sqlc. Adapter storage dan Redis tidak boleh tercampur dengan kode logika bisnis use-case.

### 8.5 Commit Dependencies
- Commit 02 bergantung pada 01. Commit 03 dan 04 dapat dieksekusi secara independen setelah 01.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Perbedaan engine Redis vs Memurai pada perintah spesifik di Windows | Gagal eksekusi perintah Redis di dev | Rendah | Batasi penggunaan perintah Redis hanya pada perintah dasar (`GET`, `SET`, `DEL`, `INCRBY`, `EXPIRE`). |
| Database migration lock saat dev runner mati mendadak | Skema dirty state | Sedang | Dokumentasikan perintah `migrate force` pada panduan troubleshooting. |

---

## 10. Definition of Done

- [ ] Seluruh 18 tabel dan relasi berhasil dimigrasikan ke PostgreSQL 16.
- [ ] `sqlc generate` menghasilkan kode Go type-safe tanpa peringatan.
- [ ] Koneksi Redis Go dan Python berfungsi dengan verifikasi TTL.
- [ ] Storage resolver berhasil memetakan path dan menolak traversal attack.
- [ ] Seluruh integration test infrastruktur lulus tanpa error.
