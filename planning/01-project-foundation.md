# Plan 01: Project Foundation & Environment Setup

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-001 |
| Name | Project Foundation & Multi-Runtime Environment Setup |
| Priority | P0 – Critical |
| Complexity | Medium |
| Status | Planned |
| Depends On | Dokumen Arsitektur & Pedoman Desain |
| Related Requirements | ARCH-01 (Monorepo), ARCH-02 (Tech Stack), SEC-08 (Secrets & Environment) |

### 1.1 Objective
Membangun struktur dasar monorepo yang bersih, terisolasi, dan siap pakai untuk tiga aplikasi utama (`apps/web`, `apps/api`, `apps/engine`), dependensi shared tooling, serta skrip otomasi pengembang di lingkungan Windows native tanpa ketergantungan pada Docker atau WSL.

### 1.2 Scope
**In Scope:**
- Inisialisasi struktur direktori monorepo sesuai `Architecture.md` Bagian 3.1.
- Inisialisasi Go Module (`apps/api`) dengan Go 1.23+ dan dependency baseline (Gin, pgx v5, go-redis, slog).
- Inisialisasi Python Virtual Environment & FastAPI project (`apps/engine`) dengan Python 3.12, Uvicorn, Pydantic v2, DuckDB, Pandas, PyArrow.
- Inisialisasi React TypeScript SPA (`apps/web`) dengan Vite, Tailwind CSS, Lucide React, dan token semantic CSS variables awal.
- Konfigurasi file `.env.example` terpusat dan per aplikasi tanpa memuat secret nyata.
- Penyusunan skrip PowerShell pengembang lokal (`scripts/dev.ps1`) untuk menjalankan ketiga runtime bersamaan.

**Out of Scope:**
- Penulisan business logic domain atau tabel database aplikasi.
- Konfigurasi Docker, Kubernetes, atau pipeline CI/CD cloud.
- Setup tool observabilitas terdistribusi (Sentry/OpenTelemetry).

### 1.3 Requirements Traceability
| Requirement ID | Deskripsi Kebutuhan | Rencana Implementasi | Sumber Dokumen |
|---|---|---|---|
| ARCH-MONO | Monorepo layout dengan pemisahan apps/web, apps/api, apps/engine | Struktur direktori dan module boundary | Architecture.md Bagian 3.1 |
| ARCH-LOCAL-DEV | Lingkungan lokal Windows native tanpa Docker/WSL | Skrip PowerShell dan local service port pinning | Architecture.md Bagian 2.4 |
| SEC-ENV-SECRETS | `.env` diabaikan Git dan `.env.example` tersanitasi | File konfigurasi environment dan `.gitignore` | Security.md Bagian 4.4 |
| DS-CODE-STD | Standar Go/Python/TS sesuai panduan engineering | Setup linter & formatter tooling baseline | Design-system.md Bagian 3 |

---

## 2. Technical Context

Sistem membutuhkan tiga runtime yang berjalan harmonis di mesin lokal pengembang Windows:
1. `apps/web`: Vite dev server pada `http://localhost:5173`, mem-proxy request `/api` ke `http://127.0.0.1:8080`.
2. `apps/api`: Go HTTP server pada `http://127.0.0.1:8080`, bertindak sebagai gerbang tunggal untuk web.
3. `apps/engine`: FastAPI Uvicorn pada `http://127.0.0.1:8000`, hanya dapat diakses melalui loopback internal oleh Go API.

Layanan pendukung lokal:
- PostgreSQL 16 pada `localhost:5432`
- Redis-compatible (Memurai) pada `localhost:6379`
- Mailpit SMTP pada `localhost:1025` dan Web UI pada `localhost:8025`

---

## 3. Implementation Tasks

### Task ID: TASK-101
**Task Name:** Monorepo Directory Scaffolding & Git Ignore Configuration
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Small |
| Status | Planned |
| Dependencies | None |
| Related Requirements | ARCH-MONO, SEC-ENV-SECRETS |

**Objective:**
Menyusun pohon folder monorepo dan memastikan folder artefak lokal serta runtime data ter-ignore secara ketat oleh Git.

**Implementation Steps:**
1. Buat direktori: `apps/web`, `apps/api`, `apps/engine`, `scripts`, `testdata`, `storage/temp`, `storage/sources`, `storage/parquet`.
2. Perbarui file `.gitignore` untuk mengabaikan `storage/`, node_modules, Python `.venv`, build artifacts Go (`bin/`), file `.env`, dan `.graphify-out/`.
3. Buat file `.env.example` di root dan template konfigurasi per layanan.

**Expected Files:**
- `.gitignore` (Proposed Path / Modification)
- `.env.example` (Proposed Path)
- `storage/.gitkeep` (Proposed Path)

**Technical Requirements:**
- Folder `storage/` sama sekali tidak boleh masuk ke commit Git.

**Testing Requirements:**
- Jalankan verifikasi file ignore untuk memastikan `storage/` dan `.env` tidak terdeteksi oleh Git.

**Acceptance Criteria:**
- Seluruh direktori target terbentuk sesuai blueprint `Architecture.md`.
- File sensitif dan storage sementara diabaikan oleh Git.

---

### Task ID: TASK-102
**Task Name:** Go Backend API Scaffolding (`apps/api`)
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-101 |
| Related Requirements | ARCH-MONO, DS-CODE-STD |

**Objective:**
Menginisialisasi Go module dan struktur modular monolith internal dengan entrypoint server Gin minimal dan health check.

**Implementation Steps:**
1. Di `apps/api`, inisialisasi module Go `go mod init insightflow/api`.
2. Pasang dependensi: Gin, pgx v5, go-redis, viper/envconfig, slog.
3. Buat struktur folder: `cmd/api/main.go`, `internal/config`, `internal/router`, `internal/middleware`, `internal/platform/httpx`.
4. Implementasikan endpoint health check publik `GET /healthz` yang mengembalikan status sistem JSON.

**Expected Files:**
- `apps/api/go.mod`
- `apps/api/cmd/api/main.go`
- `apps/api/internal/config/config.go`
- `apps/api/internal/router/router.go`

**Technical Requirements:**
- Go minimum version >= 1.23.
- Konfigurasi tervalidasi saat startup melalui struct bertipe.
- Logging menggunakan `log/slog` dengan format JSON terstruktur.

**Testing Requirements:**
- `go test ./...` pada `apps/api` untuk memastikan package terkompilasi bersih.

**Acceptance Criteria:**
- Server Go dapat dijalankan dan merespons `GET /healthz` dengan HTTP 200 `{ "status": "ok" }`.

---

### Task ID: TASK-103
**Task Name:** Python Data Engine Scaffolding (`apps/engine`)
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-101 |
| Related Requirements | ARCH-MONO, DS-CODE-STD |

**Objective:**
Menginisialisasi Python 3.12 project untuk FastAPI engine dengan dependency analitik data dan health check internal.

**Implementation Steps:**
1. Di `apps/engine`, siapkan `pyproject.toml` dengan FastAPI, Uvicorn, Pydantic v2, DuckDB, Pandas, PyArrow.
2. Buat struktur folder: `app/main.py`, `app/config.py`, `app/api/v1`, `app/core`.
3. Implementasikan endpoint internal `GET /v1/healthz` yang memverifikasi kesiapan engine.
4. Terapkan binding loopback default `127.0.0.1:8000`.

**Expected Files:**
- `apps/engine/pyproject.toml`
- `apps/engine/app/main.py`
- `apps/engine/app/config.py`

**Technical Requirements:**
- Python runtime 3.12.
- Pydantic v2 untuk manajemen setting environment.

**Testing Requirements:**
- `pytest` dasar untuk memeriksa endpoint health check.

**Acceptance Criteria:**
- FastAPI berjalan pada `127.0.0.1:8000` dan merespons health check dengan sukses.

---

### Task ID: TASK-104
**Task Name:** React SPA Frontend Scaffolding (`apps/web`)
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-101 |
| Related Requirements | ARCH-MONO, DS-CODE-STD |

**Objective:**
Menginisialisasi React TypeScript SPA dengan Vite, Tailwind CSS, Lucide React, dan token desain CSS variables awal sesuai `Design.md`.

**Implementation Steps:**
1. Di `apps/web`, inisialisasi Vite template `react-ts`.
2. Pasang dependensi: React Router, TanStack Query, Zustand, Lucide React, Tailwind CSS, clsx, tailwind-merge.
3. Konfigurasi `vite.config.ts` dengan proxy dev server `/api` ke `http://127.0.0.1:8080`.
4. Konfigurasi `index.css` dengan token warna semantic CSS variables (Light & Dark theme) sesuai `Design.md` Bagian 4.1.
5. Siapkan struktur folder: `src/app`, `src/routes`, `src/layouts`, `src/components/ui`, `src/services/apiClient.ts`.

**Expected Files:**
- `apps/web/package.json`
- `apps/web/vite.config.ts`
- `apps/web/src/index.css`
- `apps/web/src/App.tsx`

**Technical Requirements:**
- TypeScript strict mode aktif (`noImplicitAny: true`, `strictNullChecks: true`).
- Token Tailwind merujuk CSS variables, bukan nilai hex langsung.

**Testing Requirements:**
- `npm run build` berhasil tanpa type error.

**Acceptance Criteria:**
- Vite dev server dapat dijalankan pada `http://localhost:5173`, menampilkan halaman awal dengan styling semantic tokens.

---

### Task ID: TASK-105
**Task Name:** Local Development Automation Scripts
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Small |
| Status | Planned |
| Dependencies | TASK-102, TASK-103, TASK-104 |
| Related Requirements | ARCH-LOCAL-DEV |

**Objective:**
Menyediakan skrip PowerShell untuk memverifikasi prasyarat layanan lokal (PostgreSQL, Redis, Mailpit) dan menjalankan aplikasi secara terorkestrasi.

**Implementation Steps:**
1. Buat skrip `scripts/check-env.ps1` untuk memverifikasi ketersediaan port `5432`, `6379`, `1025`, serta runtime Go, Python, dan Node.
2. Buat skrip `scripts/dev.ps1` untuk menjalankan Go API, Python Engine, dan Vite dev server secara bersamaan di terminal Windows.

**Expected Files:**
- `scripts/check-env.ps1`
- `scripts/dev.ps1`

**Technical Requirements:**
- Native PowerShell tanpa ketergantungan bash/sh.

**Testing Requirements:**
- Jalankan skrip di PowerShell Windows dan verifikasi output status port.

**Acceptance Criteria:**
- Developer dapat menjalankan `scripts/dev.ps1` untuk memulai development environment dengan satu perintah.

---

## 4. API & Database Changes

### API Changes
| Method | Endpoint | Purpose | Authentication |
|---|---|---|---|
| GET | `/healthz` | Go API public health check | None |
| GET | `/v1/healthz` | Python Engine internal health check | None (Internal loopback only) |

### Database Changes
`Not Applicable` (Skema database akan diimplementasikan pada Plan 02).

---

## 5. Design & UI Implementation

- **Related Page:** Halaman placeholder bootstrap pada `apps/web`.
- **Design Tokens:** Definisi CSS variables awal pada `index.css`: `--background`, `--surface`, `--border`, `--foreground`, `--primary`, `--ring` untuk mode terang dan gelap sesuai `Design.md` Bagian 4.1.
- **Responsive Behavior:** Konfigurasi breakpoint Tailwind standar (`sm: 640px`, `md: 768px`, `lg: 1024px`, `xl: 1280px`).
- **Accessibility Requirements:** Fokus outline token `--ring` dengan offset 2px.

---

## 6. Security Requirements

- Validasi ketat bahwa Python Engine hanya mengikat IP loopback `127.0.0.1` dan tidak terekspos ke interface publik.
- File `.env` wajib dimasukkan ke `.gitignore`; template `.env.example` tidak boleh memuat secret nyata atau kredensial produksi.
- Storage folder diatur dengan path key server-side dan diabaikan dari source control.

---

## 7. Testing & Verification

- **Unit Tests:** Tes kompilasi package Go (`go test ./...`) dan tes endpoint dasar FastAPI (`pytest`).
- **Type Checking:** `tsc --noEmit` pada `apps/web` lolos tanpa error.
- **Verification Commands:**
  - Go: `cd apps/api && go test ./...`
  - Python: `cd apps/engine && pytest`
  - Web: `cd apps/web && npm run build`
  - Env check: `powershell -ExecutionPolicy Bypass -File scripts/check-env.ps1`

---

## 8. Commit Plan

### 8.1 Commit Strategy
Membuat pondasi monorepo secara berurutan: root setup -> backend Go -> Python Engine -> frontend Web -> skrip otomasi pengembang.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-101 | chore | repo | chore(repo): initialize monorepo directory layout and gitignore rules |
| 02 | TASK-102 | feat | api | feat(api): scaffold Go modular monolith and health check endpoint |
| 03 | TASK-103 | feat | engine | feat(engine): scaffold FastAPI data engine with health check |
| 04 | TASK-104 | feat | web | feat(web): scaffold React Vite SPA with semantic design tokens |
| 05 | TASK-105 | chore | scripts | chore(scripts): add local development orchestration PowerShell scripts |

### 8.3 Commit Details
- **Commit 01:** Membuat struktur direktori monorepo, file `.gitignore`, dan `.env.example`.
- **Commit 02:** Menginisialisasi `go.mod`, konfigurasi server Gin, dan handler `GET /healthz`.
- **Commit 03:** Menyiapkan `pyproject.toml`, FastAPI app instance, dan route internal `GET /v1/healthz`.
- **Commit 04:** Menyiapkan `package.json`, konfigurasi Tailwind dengan semantic tokens, dan proxy Vite.
- **Commit 05:** Menambahkan skrip verifikasi environment dan launcher pengembang lokal di `scripts/`.

### 8.4 Commit Boundaries
- Perubahan tooling Go, Python, dan Node dipisahkan dalam commit masing-masing agar mudah dilacak jika terjadi masalah build.

### 8.5 Commit Dependencies
- Commit 02, 03, dan 04 bergantung pada Commit 01. Commit 05 bergantung pada 02, 03, dan 04.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Perbedaan versi runtime Python (3.12) atau Go (1.23+) pada mesin dev | Error kompilasi atau dependency mismatch | Sedang | Skrip `check-env.ps1` memeriksa versi minimum sebelum dev server dijalankan. |
| Port conflict pada host pengembang (5173, 8080, 8000) | Gagal start dev server | Rendah | Konfigurasi port tersentralisasi dalam `.env` dengan fallback pesan error informatif. |

---

## 10. Definition of Done

- [ ] Struktur direktori monorepo terbentuk lengkap.
- [ ] Go API berjalan dan mengembalikan 200 OK pada `/healthz`.
- [ ] Python Engine berjalan dan mengembalikan 200 OK pada `/v1/healthz`.
- [ ] React Vite SPA terkompilasi bersih tanpa TypeScript error dan token CSS variables aktif.
- [ ] File `.env` dan `storage/` terverifikasi aman dalam `.gitignore`.
- [ ] Skrip `scripts/dev.ps1` berhasil dijalankan di Windows PowerShell.
