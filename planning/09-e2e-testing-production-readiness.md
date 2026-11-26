# Plan 09: End-to-End Verification & Production Readiness

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-009 |
| Name | End-to-End Verification, Performance Benchmarks & Production Readiness |
| Priority | P1 – High |
| Complexity | Medium |
| Status | Planned |
| Depends On | PLAN-001 sampai PLAN-008 |
| Related Requirements | Seluruh PRD & Security Requirements (Verifikasi Integrasi Sistem Penuh) |

### 1.1 Objective
Memvalidasi fungsionalitas keseluruhan aplikasi InsightFlow AI secara komprehensif dari hulu ke hilir: pengujian journey pengguna terintegrasi (registrasi -> OTP -> upload dataset -> profiling -> explorer -> tanya jawab AI ter-grounding -> simpan widget -> re-run), pengujian batas performa dataset (50 MB CSV / 20 MB XLSX), audit kepatuhan keamanan monorepo, serta penyusunan panduan kesiapan deployment dan verifikasi checklist sebelum rilis.

### 1.2 Scope
**In Scope:**
- Pengujian Journey Pengguna End-to-End (E2E) Otomatis:
  - Menggunakan suite pengujian integrasi berbasis mock LLM deterministik dan Mailpit lokal.
  - Skenario 1: Onboarding Pengguna Baru (Pendaftaran, tantangan OTP via Mailpit, login sukses, inisialisasi workspace pertama).
  - Skenario 2: Dataset Ingestion & Profiling (Upload file CSV sampel e-commerce, worker memproses ke Parquet, verifikasi profil statistik kualitas data).
  - Skenario 3: Data Explorer Virtual (Paginasi baris data, sorting kolom, dan filter nilai melalui AG Grid).
  - Skenario 4: Conversational Analysis Loop (Pengajuan pertanyaan analitik bahasa alami, verifikasi pembuatan AnalysisPlan, eksekusi tool DuckDB, narasi ter-grounding, dan streaming event SSE).
  - Skenario 5: Visualisasi & Dashboard Persistence (Menyimpan chart ke dashboard baru, memindahkan posisi widget pada grid, memicu tombol re-run untuk memperbarui data).
  - Skenario 6: Proteksi Batas Kuota & Isolasi Tenant (Verifikasi isolasi resource lintas workspace menghasilkan 404 seragam, dan percobaan run analisis ke-51 tertolak kuota).
- Pengujian Batas & Beban (Stress / Boundary Tests):
  - Uji upload file CSV tepat 50 MB dan file XLSX tepat 20 MB.
  - Verifikasi batasan memori DuckDB (tetap di bawah 1 GB RAM saat profiling dataset besar).
  - Verifikasi timeout eksekusi query (timeout 5 detik pada explorer, timeout 30 detik pada analitik).
  - Verifikasi sinyal bangun worker job (Plan 10): upload ke worker idle diproses < 200 ms, dan dengan Redis dimatikan job tetap diproses ≤ 2 detik lewat polling tanpa upload gagal.
- Audit Keamanan & Kepatuhan Monorepo:
  - Pemeriksaan kerentanan dependensi (`govulncheck` untuk Go, `pip-audit` untuk Python, `npm audit` untuk React).
  - Pemindaian kebocoran kredensial statis (verifikasi tidak ada hardcoded secret di seluruh codebase).
- Panduan Operasional & Kesiapan Produksi:
  - Dokumentasi konfigurasi environment produksi (`.env.production.example`).
  - Prosedur migrasi database zero-downtime dan verifikasi health check pasca-deployment.

**Out of Scope:**
- Setup load testing berskala masif terdistribusi (>10.000 user konkuren).
- Pengujian infrastruktur multi-region cloud.
- Penetrasi keamanan eksternal pihak ketiga (disiapkan untuk fase audit sertifikasi formal).

### 1.3 Requirements Traceability
| Requirement ID | Deskripsi Kebutuhan | Rencana Implementasi | Sumber Dokumen |
|---|---|---|---|
| E2E-ONBOARDING | Alur lengkap registrasi, OTP, dan login workspace | Test suite integrasi E2E skenario 1 | PRD Bagian 9 & 10 |
| E2E-PIPELINE | Alur upload, worker Parquet, dan profil kualitas | Test suite integrasi E2E skenario 2 | PRD Bagian 14-19 |
| E2E-AI-LOOP | Tanya jawab AI, verifikasi grounding, dan SSE | Test suite integrasi E2E skenario 4 | PRD Bagian 22-27 |
| E2E-DASHBOARD | Visualisasi Recharts dan grid dashboard interaktif | Test suite integrasi E2E skenario 5 | PRD Bagian 28-31 |
| SEC-CHECKLIST | Verifikasi 12 kontrol keamanan baseline Security.md | Audit checklist keamanan otomatis | Security.md Bagian 5 |

---

## 2. Technical Context

Pengujian E2E dijalankan pada lingkungan lokal yang merefleksikan konfigurasi produksi namun menggunakan komponen tiruan (*test doubles*) untuk layanan eksternal:
- **Provider LLM:** Menggunakan `MockLLMClient` di Python Engine yang mengembalikan respon JSON `AnalysisPlan` dan teks narasi terstruktur yang dapat diprediksi tanpa memotong biaya API komersial.
- **SMTP Server:** Menggunakan instance lokal Mailpit pada port `1025` dengan verifikasi penerimaan email melalui API HTTP Mailpit `http://localhost:8025/api/v1/messages`.
- **Database & Cache:** Menggunakan database PostgreSQL test terpisah (`insightflow_test`) dan Redis namespace test (`test:*`) yang dibersihkan sebelum dan sesudah pengujian.

---

## 3. Implementation Tasks

### Task ID: TASK-901
**Task Name:** End-to-End User Journey Integration Test Suite
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Large |
| Status | Planned |
| Dependencies | PLAN-003, PLAN-004, PLAN-005, PLAN-006, PLAN-007, PLAN-008 |
| Related Requirements | E2E-ONBOARDING, E2E-PIPELINE, E2E-AI-LOOP, E2E-DASHBOARD |

**Objective:**
Membangun test suite integrasi otomatis yang mengeksekusi 6 skenario inti perjalanan pengguna dari autentikasi hingga dashboard visualisasi.

**Implementation Steps:**
1. Di `apps/api/test/e2e`, buat harness pengujian integrasi Go.
2. Implementasikan Skenario 1 (Auth & Workspace):
   - Mendaftarkan user baru via API.
   - Mengambil OTP terbaru dari Mailpit API.
   - Memverifikasi OTP, mendapatkan cookie refresh token, dan access token JWT.
   - Membuat workspace baru.
3. Implementasikan Skenario 2 (Ingest & Profile):
   - Mengunggah file CSV uji (`testdata/sample_sales.csv`).
   - Melakukan polling status hingga status dataset menjadi `ready`.
   - Mengambil profil dataset dan memverifikasi kelengkapan metrik statistik kolom.
4. Implementasikan Skenario 3 (Explorer):
   - Meminta 20 baris data pertama dengan filter dan sorting kolom.
   - Memverifikasi kecocokan nilai data tabular.
5. Implementasikan Skenario 4 (AI Analysis):
   - Membuat sesi analisis baru.
   - Mengirim pertanyaan analisis via stream SSE.
   - Membaca event stream: memvalidasi event `plan_generated`, `tool_executed`, `chart_ready`, dan `run_completed`.
   - Memverifikasi narasi bebas halusinasi.
6. Implementasikan Skenario 5 (Dashboard):
   - Menyimpan hasil analisis ke dashboard baru.
   - Memperbarui layout grid.
   - Memanggil endpoint re-run widget dan memverifikasi data terbarui.
7. Implementasikan Skenario 6 (Isolasi & Kuota):
   - Mendaftarkan User B dan mencoba mengakses dataset User A (verifikasi respons 404).

**Expected Files:**
- `apps/api/test/e2e/e2e_suite_test.go`
- `apps/api/test/e2e/scenario_onboarding_test.go`
- `apps/api/test/e2e/scenario_analytics_test.go`
- `testdata/sample_sales.csv`

**Technical Requirements:**
- Pengujian harus dapat dijalankan secara mandiri dengan satu perintah di Windows PowerShell.

**Testing Requirements:**
- Seluruh 6 skenario E2E berjalan berurutan dan lulus tanpa kegagalan asersi.

**Acceptance Criteria:**
- Seluruh alur perjalanan pengguna terbukti berfungsi secara harmonis lintas Go API, Python Engine, dan database.

---

### Task ID: TASK-902
**Task Name:** Resource Limit & Boundary Stress Verification
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-901 |
| Related Requirements | E2E-PIPELINE |

**Objective:**
Menguji ketahanan sistem terhadap batas maksimum upload file, konsumsi memori DuckDB, dan kepatuhan timeout eksekusi query.

**Implementation Steps:**
1. Buat generator file uji sintetis:
   - File CSV 50 MB (berisi ~500.000 baris data penjualan).
   - File XLSX 20 MB.
2. Uji ingestion batas maksimum:
   - Unggah file 50 MB CSV: pantau penggunaan RAM proses Go dan Python Engine (pastikan RAM Python Engine tidak melebihi batas 1 GB).
   - Verifikasi waktu konversi ke Parquet selesai dalam batas toleransi wajar (<30 detik pada mesin dev).
3. Uji timeout query:
   - Simulasikan query explorer yang memakan waktu lama untuk memastikan timeout 5 detik tertangkap rapi dengan kode error `QUERY_TIMEOUT`.
4. Uji penolakan file melebihi batas:
   - Unggah file CSV 51 MB: pastikan ditolak di pintu masuk (HTTP 413) sebelum file selesai di-stream seluruhnya.

**Expected Files:**
- `apps/api/test/benchmark/boundary_test.go`
- `scripts/generate-test-data.py`

**Technical Requirements:**
- Engine DuckDB harus tetap stabil dan membatasi alokasi thread maksimal 2.

**Testing Requirements:**
- Jalankan benchmark test dan catat metrik durasi pemrosesan serta puncak memori RAM.

**Acceptance Criteria:**
- Sistem mampu memproses file batas kapasitas MVP tanpa OOM crash dan menolak file yang melebihi batas secara elegan.

---

### Task ID: TASK-903
**Task Name:** Security Audit Checklist & Static Code Scans
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Small |
| Status | Planned |
| Dependencies | PLAN-001 sampai PLAN-008 |
| Related Requirements | SEC-CHECKLIST |

**Objective:**
Menjalankan audit pemindaian keamanan otomatis terhadap dependensi monorepo dan memverifikasi kepatuhan terhadap 12 baseline kontrol keamanan `Security.md`.

**Implementation Steps:**
1. Jalankan pemindai kerentanan dependensi:
   - Go: `govulncheck ./...` di folder `apps/api`.
   - Python: `pip-audit` di folder `apps/engine`.
   - Node: `npm audit --omit=dev` di folder `apps/web`.
2. Lakukan pemindaian kebocoran kredensial (secret detection) di seluruh repository untuk memastikan tidak ada token, password, atau API key yang tertinggal dalam commit.
3. Buat skrip verifikasi otomatis `scripts/verify-security-baseline.ps1`:
   - Memeriksa flag cookie `HttpOnly` dan `SameSite=Lax`.
   - Memeriksa bahwa tidak ada penggunaan `eval()`, `exec()`, atau `dangerouslySetInnerHTML`.
   - Memeriksa konfigurasi loopback internal `127.0.0.1` pada Engine.

**Expected Files:**
- `scripts/verify-security-baseline.ps1`
- `docs/security-audit-report.md`

**Technical Requirements:**
- Tidak boleh ada kerentanan berstatus *High* atau *Critical* pada dependensi yang digunakan.

**Testing Requirements:**
- Eksekusi skrip audit keamanan dan pastikan seluruh checklist menghasilkan status PASS.

**Acceptance Criteria:**
- Codebase bersih dari secret, dependensi bebas dari celah keamanan kritis, dan kontrol baseline terverifikasi.

---

### Task ID: TASK-904
**Task Name:** Production Readiness Guide & Deployment Configuration
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Small |
| Status | Planned |
| Dependencies | TASK-903 |
| Related Requirements | ARCH-LOCAL-DEV, SEC-ENV-SECRETS |

**Objective:**
Menyusun template konfigurasi lingkungan produksi, prosedur migrasi database, dan checklist operasional sebelum sistem dirilis ke produksi.

**Implementation Steps:**
1. Buat file template environment produksi `.env.production.example` memuat seluruh variabel wajib dengan nilai placeholder yang aman:
   - `DATABASE_URL`, `REDIS_URL`, `JWT_SECRET`, `ENGINE_INTERNAL_TOKEN`, `LLM_API_KEY`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `SMTP_HOST`, `STORAGE_ROOT_DIR`.
2. Dokumentasikan prosedur migrasi database rilis (`migrate -path db/migrations -database ... up`).
3. Dokumentasikan health check probes untuk monitoring infrastruktur:
   - Liveness probe: `GET /healthz` (Go API).
   - Readiness probe: verifikasi konektivitas PostgreSQL dan Redis.
4. Buat dokumen checklist kesiapan rilis `docs/production-readiness-checklist.md`.

**Expected Files:**
- `.env.production.example`
- `docs/production-readiness-checklist.md`

**Technical Requirements:**
- Semua rahasia wajib bersumber dari environment variables terenkripsi, bukan dari konfigurasi statis.

**Testing Requirements:**
- Verifikasi bahwa seluruh variabel lingkungan pada `.env.production.example` memiliki validator tipe pada startup Go API dan Python Engine.

**Acceptance Criteria:**
- Panduan operasional dan template environment produksi lengkap, jelas, dan siap digunakan oleh tim infrastruktur.

---

## 4. API & Database Changes

### API Changes
`Not Applicable` (Tahap verifikasi dan audit).

### Database Changes
`Not Applicable` (Menggunakan database test terisolasi).

---

## 5. Design & UI Implementation
`Not Applicable`

---

## 6. Security Requirements

- Strict Clean Up: Database test dan file penyimpanan sementara pada folder `storage/temp/` yang dihasilkan selama pengujian E2E wajib dibersihkan secara otomatis di akhir pengujian.
- Secret Sanitization: Kunci API mock atau kredensial yang digunakan dalam test suite dilarang menggunakan format kredensial produksi nyata.
- Zero Vulnerability: Menolak rilis jika terdapat temuan celah keamanan berkategori *High* atau *Critical* pada dependensi sistem.

---

## 7. Testing & Verification

- **Comprehensive Verification Commands:**
  - Audit Go: `cd apps/api && govulncheck ./...`
  - Audit Python: `cd apps/engine && pip-audit`
  - Audit Web: `cd apps/web && npm audit`
  - Test E2E Penuh: `powershell -ExecutionPolicy Bypass -File scripts/run-e2e-tests.ps1`
  - Audit Baseline Keamanan: `powershell -ExecutionPolicy Bypass -File scripts/verify-security-baseline.ps1`
  - Regresi sinyal job (Plan 10): `cd apps/api; go test ./internal/modules/datasets/... ./internal/modules/jobs/... ./internal/platform/cache/...` dengan `TEST_DATABASE_URL` dan `TEST_REDIS_ADDR` aktif, lalu ulangi dengan Redis dimatikan untuk memverifikasi fail-open.

---

## 8. Commit Plan

### 8.1 Commit Strategy
Memisahkan test harness E2E -> tes beban batas dataset -> skrip audit keamanan -> dokumentasi kesiapan rilis dan template produksi.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-901 | test | e2e | test(e2e): implement end-to-end user journey integration test suite |
| 02 | TASK-902 | test | benchmark | test(benchmark): add dataset size boundary and DuckDB memory stress tests |
| 03 | TASK-903 | chore | security | chore(security): add automated security baseline audit script and dependency scans |
| 04 | TASK-904 | docs | ops | docs(ops): create production readiness checklist and environment templates |

### 8.3 Commit Details
- **Commit 01:** Menyertakan suite tes E2E di `apps/api/test/e2e` dan dataset uji sampel.
- **Commit 02:** Menyertakan skrip pembuat data uji sintetis dan benchmark beban memori.
- **Commit 03:** Menyertakan skrip PowerShell audit keamanan dan laporan audit dependensi.
- **Commit 04:** Menyertakan template `.env.production.example` dan checklist operasional produksi.

### 8.4 Commit Boundaries
- Tahap pengujian dan audit kesiapan rilis dieksekusi setelah seluruh kode modul fitur (Plan 01–08) selesai diimplementasikan.

### 8.5 Commit Dependencies
- Bergantung pada keberhasilan implementasi seluruh Plan 01 sampai Plan 08.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Flaky tests pada streaming SSE di lingkungan Windows dev yang lambat | E2E test gagal secara acak | Sedang | Gunakan retry toleran dan timeout dinamis pada pembacaan stream event di test suite. |
| Perbedaan konfigurasi environment lokal vs server produksi Linux | Kegagalan deployment saat rilis | Sedang | Pastikan seluruh path filesystem dibangun menggunakan fungsi abstraksi path os-agnostik (`filepath.Join` dan `pathlib.Path`). |

---

## 10. Definition of Done

- [ ] Seluruh 6 skenario E2E integrasi berjalan lancar dan menghasilkan status lulus 100%.
- [ ] Upload file 50 MB CSV dan 20 MB XLSX berhasil diproses tanpa membengkakkan memori (>1 GB RAM).
- [ ] Audit dependensi `govulncheck`, `pip-audit`, dan `npm audit` bebas dari celah keamanan kritis.
- [ ] Codebase bebas dari hardcoded secrets atau kebocoran kredensial.
- [ ] Template konfigurasi produksi dan checklist kesiapan rilis terdokumentasi lengkap.
