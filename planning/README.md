# InsightFlow AI — Implementation Planning Index

## 1. Project Overview

InsightFlow AI adalah platform analitik data mandiri yang dirancang bagi pengguna non-teknis (pemilik UMKM, analis pemula, peneliti, dan konsultan freelance). Pengguna mengunggah dataset tabular (CSV/XLSX), memeriksa profil kualitas data, mengeksplorasi data mentah, mengajukan analisis dalam bahasa alami, menerima komputasi deterministik berbasis DuckDB dengan penjelasan LLM yang tervalidasi, serta menyimpan visualisasi ke dalam dashboard interaktif.

Sistem diimplementasikan dalam arsitektur **monorepo**:
- **Frontend SPA:** React 18+, TypeScript strict, Vite, Tailwind CSS, shadcn/ui, AG Grid Community, Recharts.
- **Backend API Gateway:** Go (Gin), modular monolith (`handler -> service -> repository`), otorisasi workspace, job orchestration.
- **Internal Data/AI Engine:** Python 3.12 (FastAPI), DuckDB (read-only Parquet query), Pandas (agregat kecil), Pydantic contracts, LLM integration.
- **Database & Storage:** PostgreSQL 16 (metadata transaksional), Redis (state efemeral OTP/OAuth, cache deterministik ber-TTL), Local Filesystem (source raw & immutable Parquet).

---

## 2. Planning Objectives

Kumpulan rencana implementasi ini dirancang untuk:
1. Menerjemahkan seluruh spesifikasi dalam `PRD_InsightFlow_AI.md`, `Architecture.md`, `Security.md`, `Design-system.md`, dan `Design.md` menjadi paket kerja modular yang siap dieksekusi.
2. Memastikan seluruh invariant keamanan (*Zero Trust*, isolasi per workspace, fail-closed auth, sanitasi output LLM, no raw data to external LLM) diintegrasikan langsung pada fase terkait.
3. Menyediakan **Commit Plan** terstruktur per file dengan konvensi Conventional Commits sehingga setiap perubahan terisolasi, teruji, dan mudah di-review.
4. Memandu AI coding agent dan engineer untuk bekerja tanpa mengubah ruang lingkup, mengarang command, atau mengabaikan dependency teknis.

---

## 3. Documentation References

| Dokumen | Lokasi | Peran dalam Implementasi |
|---|---|---|
| Product Requirements Document | [`../docs/core_docs/PRD_InsightFlow_AI.md`](../docs/core_docs/PRD_InsightFlow_AI.md) | Otoritas utama kebutuhan bisnis, acceptance criteria, dan alur fitur. |
| System Architecture | [`../docs/core_docs/Architecture.md`](../docs/core_docs/Architecture.md) | Blueprint arsitektur monorepo, batas modular, dan kontrak antarlayanan. |
| Security Guide | [`../docs/core_docs/Security.md`](../docs/core_docs/Security.md) | Kontrol keamanan teknis, isolasi memori, hashing, dan otorisasi. |
| Code Style Guide | [`../docs/core_docs/Design-system.md`](../docs/core_docs/Design-system.md) | Standar struktur kode, penamaan, error handling, dan modularitas. |
| Visual & UI/UX Guide | [`../docs/core_docs/Design.md`](../docs/core_docs/Design.md) | Token visual, tema terang/gelap, layout app shell, dan pola interaksi. |
| Agent Instructions | [`../AGENTS.md`](../AGENTS.md) | Panduan operasional agen AI, batasan, skill, dan aturan verifikasi. |

---

## 4. Overall Development Approach

Pendekatan yang digunakan adalah **Phase-Based & Domain-Driven Modular Development**:
1. **Foundation & Setup:** Menginisialisasi monorepo dengan tiga aplikasi (`apps/web`, `apps/api`, `apps/engine`) dan shared tooling.
2. **Infrastructure & Persistence:** Membangun lapisan data transaksional (PostgreSQL + sqlc), cache/state (Redis), dan resolver penyimpanan lokal.
3. **Identity & Security Boundary:** Membangun autentikasi aman (Argon2id, OTP HMAC Redis, Google OIDC PKCE) dan kontrol akses workspace sebelum modul bisnis dibangun.
4. **Data Ingestion & Profiling:** Menangani upload streaming, antrean background job PostgreSQL, normalisasi Parquet, dan kalkulasi profil DuckDB.
5. **Data Explorer:** Menampilkan data mentah terpaginasi virtual melalui AG Grid dengan otorisasi ketat.
6. **AI Analysis & Computation:** Membangun loop analisis terstruktur: schema context -> planner LLM -> validasi argumen -> komputasi DuckDB -> result envelope -> narrator LLM -> grounding checker -> SSE relay.
7. **Visualization & Dashboards:** Merender chart Recharts dari `ChartSpec` tervalidasi dan grid dashboard 12 kolom yang dapat dihitung ulang.
8. **Governance & Hardening:** Audit trail, pembatasan kuota/rate, penanganan error tersanitasi, dan verifikasi fail-closed.
9. **End-to-End Verification & Production Readiness:** Menjalankan pengujian integrasi lintas aplikasi dan validasi kesiapan deployment.

---

## 5. Master Planning Index

| Order | Plan File | Scope & Domain | Priority | Status | Dependencies |
|---|---|---|---|---|---|
| 00 | [`00-roadmap.md`](00-roadmap.md) | Global Roadmap, Milestones, Mermaid DAG, & Requirements Matrix | P0 | Planned | Seluruh dokumen core_docs |
| 01 | [`01-project-foundation.md`](01-project-foundation.md) | Monorepo layout, Go module, FastAPI app, Vite React, dev scripts | P0 | Planned | Tidak ada |
| 02 | [`02-database-infrastructure.md`](02-database-infrastructure.md) | PostgreSQL schema, golang-migrate, sqlc queries, Redis client, storage resolver | P0 | Planned | Plan 01 |
| 03 | [`03-authentication-authorization.md`](03-authentication-authorization.md) | Email/Password, OTP Redis/SMTP, Google OIDC PKCE, JWT tokens, workspace RBAC | P0 | Planned | Plan 02 |
| 04 | [`04-dataset-pipeline-profiling.md`](04-dataset-pipeline-profiling.md) | Multipart upload, PostgreSQL job worker, DuckDB ingestion, Parquet, profiling | P0 | Planned | Plan 02, Plan 03 |
| 05 | [`05-data-explorer.md`](05-data-explorer.md) | Server-side pagination DuckDB, AG Grid frontend, version metadata | P1 | Planned | Plan 04 |
| 06 | [`06-ai-analysis-engine.md`](06-ai-analysis-engine.md) | SSE streaming, LLM planner, deterministic tools, grounding check, narrative | P0 | Planned | Plan 04 |
| 07 | [`07-dashboards-visualizations.md`](07-dashboards-visualizations.md) | Recharts ChartRenderer, 12-col dashboard grid, widget re-run, saved results | P1 | Planned | Plan 06 |
| 08 | [`08-governance-audit-hardening.md`](08-governance-audit-hardening.md) | Usage tracking, daily quota, audit logs, rate limiters, error sanitization | P1 | Planned | Plan 03, Plan 06 |
| 09 | [`09-e2e-testing-production-readiness.md`](09-e2e-testing-production-readiness.md) | E2E journeys (Mailpit + fake LLM), benchmark dataset, deployment checklist | P1 | Planned | Plan 01-08 |

---

## 6. Priority & Complexity Definitions

### Prioritas
- **P0 – Critical:** Komponen atau fitur pondasi mutlak yang menjadi prasyarat berjalannya alur kerja utama aplikasi.
- **P1 – High:** Fitur operasional inti yang wajib tersedia untuk memenuhi kriteria MVP.
- **P2 – Medium:** Fitur pendukung atau nilai tambah yang dijadwalkan setelah jalur kritis selesai.
- **P3 – Low:** Peningkatan kosmetik atau optimasi non-esensial.

### Kompleksitas
- **Small:** Perubahan lokal pada 1–3 file dengan dependensi minimal.
- **Medium:** Melibatkan koordinasi antar-layer dalam satu modul (handler, service, repository, atau UI feature).
- **Large:** Melibatkan kontrak lintas aplikasi (Go API <-> Python Engine <-> React SPA) atau integrasi multi-komponen sistem.

---

## 7. Progress Tracking & Conventions

Status implementasi setiap task dan fase diklasifikasikan sebagai:
- `Planned`: Belum dimulai (status default saat ini).
- `In Progress`: Sedang dalam pengerjaan aktif.
- `Blocked`: Terhenti oleh dependensi prasyarat atau isu teknis.
- `Under Review`: Implementasi selesai, menunggu pengujian/review.
- `Completed`: Kode telah diuji, diverifikasi, dan memenuhi seluruh *Acceptance Criteria*.
- `Cancelled`: Dibatalkan berdasarkan perubahan kebutuhan/ADR yang disepakati.

---

## 8. Commit Planning Conventions

Setiap file implementasi wajib menyertakan **Commit Plan** yang memetakan pekerjaan ke dalam commit terpisah yang logis dengan format Conventional Commits:
`type(scope): subject`

Tipe yang diizinkan:
- `feat`: Penambahan kapabilitas atau fungsionalitas baru.
- `fix`: Perbaikan bug atau penanganan error.
- `refactor`: Restrukturisasi kode tanpa mengubah fungsionalitas eksternal.
- `test`: Penambahan atau pembaruan unit/integration test.
- `docs`: Pembaruan dokumentasi atau spesifikasi.
- `build`: Perubahan tooling, dependency manifest, atau konfigurasi monorepo.
- `chore`: Pekerjaan pemeliharaan rutin.

**Aturan Commit:**
1. Dependensi dan skema database harus di-commit sebelum kode implementasi bisnis yang menggunakannya.
2. Business logic backend harus terpisah dari implementasi komponen UI.
3. Commit tidak boleh membiarkan build atau test dalam kondisi rusak (*broken build*).
4. Tidak ada operasi commit aktual selama fase perencanaan ini.
