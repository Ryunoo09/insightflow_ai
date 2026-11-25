# Plan 08: Governance, Usage Tracking, Audit Logging & Security Hardening

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-008 |
| Name | Governance, Usage Tracking, Audit Logging & Security Hardening |
| Priority | P1 – High |
| Complexity | Medium |
| Status | Planned |
| Depends On | PLAN-003, PLAN-006 |
| Related Requirements | F-24, F-25 (Usage & Quota), F-26, F-27 (Audit & Error Sanitization), SEC-01..12 |

### 1.1 Objective
Membangun lapisan tata kelola (*governance*), pemantauan kuota pemakaian harian (*usage tracking*), pencatatan jejak audit aktivitas sensitif (*audit logging*), pembatasan laju request (*rate limiting* berbasis Redis), serta penegakan sanitasi penanganan error dan pencegahan kebocoran informasi teknis di seluruh sistem.

### 1.2 Scope
**In Scope:**
- Pencatatan Pemakaian Sumber Daya (*Usage Records*): pencatatan token input/output LLM, jumlah eksekusi run analisis, dan total byte penyimpanan file dataset per workspace.
- Penegakan Kuota Harian (*Daily Quotas*): pengecekan sisa kuota analisis harian (default 50 run/hari pada paket MVP) menggunakan counter atomic Redis dengan reset tengah malam UTC, serta pembatasan kapasitas penyimpanan dataset (maksimal 500 MB per workspace).
- Rate Limiting Middleware di Go API: token bucket berbasis Redis untuk membatasi endpoint publik (Auth: 5 req/menit per IP+Email, API umum: 60 req/menit per user, Upload: 10 req/menit per workspace) dengan respons HTTP 429 dan header `Retry-After`.
- Audit Logging Terstruktur: pencatatan event penting (`auth.login`, `auth.failed`, `dataset.upload`, `dataset.delete`, `analysis.run`, `dashboard.delete`) ke tabel `audit_logs` di PostgreSQL memuat `user_id`, `workspace_id`, `action`, `resource_id`, payload detail `jsonb`, alamat IP teranonimisasi, dan user agent.
- Sanitasi Error & Respon API Seragam: middleware error global di Go API dan exception handler di Python Engine yang memformat error ke payload JSON standar (`error: { code, message, request_id }`), menyembunyikan stack trace, pesan error internal database, dan path filesystem absolut.
- Reduksi Log & Proteksi Secret: filter logger `log/slog` dan Uvicorn yang menyaring header `Authorization`, cookie refresh token, nilai password, data OTP, dan potongan data mentah pengguna dari log konsol.

**Out of Scope:**
- Integrasi gateway pembayaran atau penagihan tagihan (*billing*) berbayar (Stripe/Midtrans - disiapkan untuk fase post-MVP).
- Pengiriman log ke SIEM eksternal pihak ketiga (seperti Datadog atau Splunk).
- Antarmuka visual penampil audit log untuk pengguna reguler (audit log dikhususkan untuk investigasi teknis dan kepatuhan sistem internal).

### 1.3 Requirements Traceability
| Requirement ID | Deskripsi Kebutuhan | Rencana Implementasi | Sumber Dokumen |
|---|---|---|---|
| F-24, F-25 | Pelacakan pemakaian token/storage & penegakan kuota harian | Modul usage Go, counter Redis & tabel DB | PRD Bagian 32 & 33 |
| F-26 | Audit log terstruktur untuk aktivitas sensitif | Middleware audit Go & tabel `audit_logs` | PRD Bagian 34 |
| F-27 | Sanitasi error global & payload error seragam | Middleware error Go & platform httpx | PRD Bagian 35 |
| SEC-RATE-LIMIT | Rate limiting terdistribusi berbasis Redis | Middleware rate limiter Go | Security.md Bagian 3.4 |
| SEC-LOG-REDACT | Reduksi secret dan token dari log sistem | Slog handler filter custom | Security.md Bagian 4.4 |

---

## 2. Technical Context

Setiap request yang masuk ke Go API melewati rantai middleware keamanan:
1. `RequestIDMiddleware`: menyematkan UUID v4 `X-Request-ID`.
2. `RateLimitMiddleware`: memeriksa kuota request di Redis (`rate:{prefix}:{identifier}`). Jika counter melebihi ambang batas, langsung merespons HTTP 429.
3. `AuthMiddleware`: memvalidasi JWT Bearer token di memori.
4. `WorkspaceMemberMiddleware`: memeriksa keanggotaan tenant secara atomik.
5. `QuotaMiddleware` (khusus rute analisis & upload): membaca penggunaan aktif di Redis. Jika kuota harian run habis (>= 50), tolak dengan kode `QUOTA_ANALYSIS_RUNS_EXCEEDED` (HTTP 403).
6. Saat operasi selesai, `AuditLogger` menyisipkan catatan aktivitas ke PostgreSQL secara asinkron atau dalam transaksi bisnis terkait.
7. Jika terjadi panic atau error runtime, `GlobalErrorMiddleware` menangkap exception, mencatat stack trace hanya di log internal dengan `X-Request-ID`, dan mengembalikan pesan yang aman dan ramah ke klien.

---

## 3. Implementation Tasks

### Task ID: TASK-801
**Task Name:** Usage Tracker & Daily Quota Guard
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-002, PLAN-003, PLAN-006 |
| Related Requirements | F-24, F-25 |

**Objective:**
Membangun modul pelacak pemakaian metrik sistem (token LLM, run analisis, storage) dan penjaga kuota harian per workspace berbasis Redis dan PostgreSQL.

**Implementation Steps:**
1. Di `apps/api/internal/modules/governance`, buat `usage_service.go` dan `quota_guard.go`.
2. Implementasikan pelacak penggunaan:
   - Catat baris baru di tabel `usage_records` saat analisis berhasil (`metric_type='llm_tokens'`, `amount=tokens`, `workspace_id`).
   - Perbarui agregasi pemakaian storage dataset saat upload selesai (`metric_type='storage_bytes'`).
3. Implementasikan `QuotaGuard`:
   - Gunakan Redis counter `quota:runs:{workspace_id}:{YYYYMMDD}` dengan TTL 86.400 detik (24 jam).
   - Operasi `CheckAndIncrementRunQuota(workspaceId)` menggunakan atomic script Lua atau transaksi Redis:
     - Jika counter < 50, lakukan `INCR` dan return `allowed = true`.
     - Jika counter >= 50, return `allowed = false`.
   - Endpoint query pemakaian: `GET /api/v1/workspaces/:workspace_id/usage` mengembalikan kuota terpakai hari ini, sisa kuota, dan estimasi storage yang digunakan.
4. Di frontend, buat komponen visual indikator kuota (*UsageQuotaIndicator*) pada navigasi samping atau header.

**Expected Files:**
- `apps/api/internal/modules/governance/usage_service.go`
- `apps/api/internal/modules/governance/quota_guard.go`
- `apps/api/internal/modules/governance/handler.go`
- `apps/web/src/features/workspaces/components/UsageQuotaIndicator.tsx`

**Technical Requirements:**
- Transaksi kuota Redis harus atomic untuk mencegah race condition bypass kuota.
- Kuota run direset tepat pada pukul 00:00:00 UTC setiap hari.

**Testing Requirements:**
- Integration test kuota: simulasikan 50 run analisis berturut-turut, pastikan run ke-51 diblokir dengan HTTP 403.

**Acceptance Criteria:**
- Pemakaian token dan run tercatat akurat, serta kuota harian ditegakkan tanpa kebocoran.

---

### Task ID: TASK-802
**Task Name:** Distributed Rate Limiting Middleware
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-002 |
| Related Requirements | SEC-RATE-LIMIT |

**Objective:**
Membangun middleware pembatas laju request (*rate limiter*) terdistribusi berbasis Redis sliding window / token bucket di Go API.

**Implementation Steps:**
1. Di `apps/api/internal/middleware/ratelimit`, buat modul `ratelimiter.go`.
2. Tentukan aturan batasan per jenis endpoint:
   - `AuthRateLimiter`: maksimal 5 request per menit per kunci `ip + email` untuk endpoint `/auth/login` dan `/auth/verify-otp`.
   - `UserRateLimiter`: maksimal 60 request per menit per `user_id` untuk endpoint API data umum.
   - `UploadRateLimiter`: maksimal 10 upload per menit per `workspace_id`.
3. Terapkan algoritma counter berjangka waktu di Redis (`INCR` + `EXPIRE`).
4. Jika batas terlampaui:
   - Hentikan request di middleware.
   - Set header response: `Retry-After: <detik>`, `X-RateLimit-Limit`, `X-RateLimit-Remaining: 0`.
   - Kembalikan respons JSON seragam HTTP 429 `RATE_LIMIT_EXCEEDED`.

**Expected Files:**
- `apps/api/internal/middleware/ratelimit/limiter.go`
- `apps/api/internal/middleware/ratelimit/rules.go`

**Technical Requirements:**
- Jika Redis tidak dapat diakses (downtime), rate limiter beroperasi dengan mode *Fail-Closed* untuk endpoint auth sensitif dan *Fail-Open* untuk endpoint read publik.

**Testing Requirements:**
- Unit test rate limiter dengan 6 request beruntun ke rute auth untuk memverifikasi munculnya HTTP 429 pada request ke-6.

**Acceptance Criteria:**
- Serangan brute force atau spam request terblokir otomatis dengan status HTTP 429.

---

### Task ID: TASK-803
**Task Name:** Structured Audit Trail Logging
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-002, PLAN-003 |
| Related Requirements | F-26 |

**Objective:**
Mengimplementasikan pencatatan jejak audit (*audit trail*) otomatis ke tabel `audit_logs` untuk seluruh aktivitas pengguna yang sensitif.

**Implementation Steps:**
1. Di `apps/api/internal/platform/audit`, buat interface dan service `AuditLogger`.
2. Daftarkan event penting yang wajib diaudit:
   - `auth.register`, `auth.login`, `auth.login_failed`, `auth.logout`
   - `workspace.create`, `workspace.member_added`
   - `dataset.upload`, `dataset.version_created`, `dataset.delete`
   - `analysis.session_created`, `analysis.run_executed`
   - `dashboard.created`, `dashboard.deleted`
3. Catat metadata: `user_id`, `workspace_id`, `action`, `resource_type`, `resource_id`, payload perubahan `details` (JSONB), `ip_address` (disanitasi), `user_agent`, `created_at`.
4. Jalankan pencatatan secara asinkron menggunakan worker channel internal di Go agar tidak membebani latency respons HTTP pengguna utama.

**Expected Files:**
- `apps/api/internal/platform/audit/logger.go`
- `apps/api/internal/platform/audit/events.go`

**Technical Requirements:**
- Record audit log bersifat *append-only*; dilarang menyediakan fungsi `UPDATE` atau `DELETE` pada tabel `audit_logs`.
- Nilai data sensitif (seperti password, OTP, token) wajib difilter sebelum dicatat ke kolom `details`.

**Testing Requirements:**
- Integration test verifikasi pencatatan audit log saat user login dan mengunggah dataset.

**Acceptance Criteria:**
- Seluruh event sensitif tercatat lengkap di PostgreSQL dengan payload detail yang aman.

---

### Task ID: TASK-804
**Task Name:** Global Error Sanitization & Sensitive Log Redaction
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Small |
| Status | Planned |
| Dependencies | PLAN-001 |
| Related Requirements | F-27, SEC-LOG-REDACT |

**Objective:**
Membangun middleware penanganan error global dan penyaring log sistem untuk memastikan tidak ada rahasia, stack trace, atau detail internal yang bocor ke klien maupun log konsol.

**Implementation Steps:**
1. Di Go API (`apps/api/internal/middleware/error.go`), buat `RecoveryAndErrorMiddleware`:
   - Tangkap seluruh panic Go.
   - Format seluruh error HTTP menjadi format JSON standar:
     ```json
     {
       "error": {
         "code": "INTERNAL_SERVER_ERROR",
         "message": "Terjadi kesalahan internal pada server. Silakan coba beberapa saat lagi.",
         "request_id": "c1f7a4b2-9a3d-4c8e-b5f1-2d3e4f5a6b7c"
       }
     }
     ```
   - Sembunyikan detail error database (seperti syntax error PostgreSQL, nama tabel internal, atau constraint name).
2. Konfigurasi `log/slog` custom handler:
   - Buat filter atribut log yang otomatis menyamarkan (*redact*) nilai field berlabel: `password`, `token`, `otp`, `secret`, `authorization`, `cookie`.
   - Ganti nilai sensitif dengan `[REDACTED]`.
3. Terapkan exception handler serupa di Python Engine (`apps/engine/app/core/errors.py`) untuk menangani error DuckDB dan Uvicorn.

**Expected Files:**
- `apps/api/internal/middleware/error.go`
- `apps/api/internal/platform/logger/redactor.go`
- `apps/engine/app/core/errors.py`

**Technical Requirements:**
- Tidak ada response API ke klien yang memuat stack trace atau path file sistem Windows (`C:\...`).

**Testing Requirements:**
- Unit test pemicu panic di Go API dan verifikasi keluaran response 500 JSON yang tersanitasi.
- Test log redactor memverifikasi header auth tersamar menjadi `[REDACTED]`.

**Acceptance Criteria:**
- Error ditangani secara elegan dan aman, log sistem bebas dari bocoran kredensial atau data mentah.

---

## 4. API & Database Changes

### API Changes
| Method | Endpoint | Purpose | Authentication |
|---|---|---|---|
| GET | `/api/v1/workspaces/:workspace_id/usage` | Melihat kuota dan pemakaian aktif | Bearer JWT + Member |

### Database Changes
Menggunakan tabel yang sudah disiapkan pada Plan 02:
- `usage_records`: `id`, `workspace_id`, `metric_type`, `amount`, `recorded_at`.
- `audit_logs`: `id`, `workspace_id`, `user_id`, `action`, `resource_type`, `resource_id`, `details`, `ip_address`, `created_at`.

---

## 5. Design & UI Implementation

- **Related Component:** `UsageQuotaIndicator.tsx` (ditempatkan pada AppShell sidebar atau header).
- **Component Requirements:**
  - Progress bar mini yang menunjukkan jumlah run analisis terpakai hari ini (misal: \"24 / 50 Analisis Digunakan\").
  - Peringatan warna visual: Normal (biru/indigo), Hampir habis >80% (amber/kuning), Habis 100% (merah/danger).
  - Modal peringatan informatif saat pengguna mencoba mengirim pertanyaan ketika kuota harian telah habis.
- **Design Tokens:** `--warning` (#F59E0B), `--danger` (#EF4444) untuk indikasi batas kuota.

---

## 6. Security Requirements

- Non-Repudiation Audit: Audit log disimpan pada tabel append-only tanpa endpoint penghapusan untuk menjamin integritas rekam jejak.
- Fail-Closed Security: Rate limiter pada rute autentikasi menolak request jika Redis down demi mencegah serangan brute force tanpa kendali.
- Privacy Preservation: Alamat IP pengguna dianonimkan (masking oktet terakhir) sebelum disimpan ke tabel audit log.

---

## 7. Testing & Verification

- **Unit Tests:**
  - Logika filter redaction slog pada Go API.
  - Skrip perhitungan sliding window rate limiter di Redis.
  - Format respon global error middleware.
- **Integration Tests:**
  - Skenario pembatasan rate limit auth (5 request gagal memicu HTTP 429).
  - Skenario habis kuota harian analisis (request ke-51 memicu HTTP 403).
  - Verifikasi tabel `audit_logs` terisi setelah aksi upload dataset.
- **Verification Commands:**
  - `cd apps/api && go test -v ./internal/middleware/ratelimit/...`
  - `cd apps/api && go test -v ./internal/modules/governance/...`
  - `cd apps/api && go test -v ./internal/platform/logger/...`

---

## 8. Commit Plan

### 8.1 Commit Strategy
Memisahkan rate limiter terdistribusi -> usage tracking & kuota harian -> audit logger terstruktur -> sanitasi error & log redaction -> indikator kuota UI.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-802 | feat | middleware | feat(middleware): implement Redis-backed distributed rate limiters |
| 02 | TASK-801 | feat | governance | feat(governance): add resource usage tracker and daily analysis quota guard |
| 03 | TASK-803 | feat | audit | feat(audit): implement structured append-only audit trail logging |
| 04 | TASK-804 | feat | platform | feat(platform): implement global error sanitization and log secret redaction |
| 05 | TASK-801 | feat | web | feat(web): add daily usage quota indicator component in AppShell |

### 8.3 Commit Details
- **Commit 01:** Menyertakan middleware rate limiting Redis untuk auth, user, dan upload.
- **Commit 02:** Menyertakan repositori usage, quota guard atomic Redis, dan endpoint query kuota.
- **Commit 03:** Menyertakan service audit logging asinkron dan pendaftaran event sensitif.
- **Commit 04:** Menyertakan middleware recovery/error terpadu dan custom logger redactor di Go dan Engine.
- **Commit 05:** Menyertakan komponen React UI penampil sisa kuota harian pada navigasi aplikasi.

### 8.4 Commit Boundaries
- Middleware keamanan dasar (rate limit dan error recovery) harus terpasang sebelum rute governance dibuka.

### 8.5 Commit Dependencies
- Commit 01 dan 02 bergantung pada Plan 02. Commit 05 bergantung pada 02.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Akumulasi data tabel `audit_logs` dan `usage_records` membengkak seiring waktu | Pertumbuhan ukuran database PostgreSQL | Rendah | Siapkan strategi retensi data (partisi tabel bulanan atau retensi 90 hari pada fase post-MVP). |
| Ketidaksesuaian waktu zona jam reset kuota harian | Pengguna bingung kapan kuota direset | Rendah | Tetapkan standar reset eksplisit pukul 00:00:00 UTC dan tampilkan waktu countdown lokal pada tooltip UI. |

---

## 10. Definition of Done

- [ ] Rate limiting memblokir brute force login pada percobaan ke-6 dengan HTTP 429.
- [ ] Kuota harian 50 run analisis ditegakkan secara tepat di backend.
- [ ] Catatan audit log append-only terbentuk pada seluruh event penting.
- [ ] Error internal server terformat bersih tanpa membocorkan stack trace atau path file lokal.
- [ ] Kredensial dan secret berhasil tersamar dari log konsol.
- [ ] Seluruh unit dan integration test governance lulus 100%.
