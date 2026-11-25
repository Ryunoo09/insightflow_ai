# Plan 03: Authentication, Authorization & Workspace Management

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-003 |
| Name | Authentication, Workspace RBAC, OTP Email & Google OIDC PKCE |
| Priority | P0 – Critical |
| Complexity | Large |
| Status | Planned |
| Depends On | PLAN-002 |
| Related Requirements | F-01, F-02 (Auth & OTP), F-38 (Google OAuth), F-04, F-05 (Workspaces), SEC-01..05 |

### 1.1 Objective
Membangun modul identitas dan kontrol akses lengkap yang mencakup alur registrasi, autentikasi email/password dengan Argon2id, verifikasi email/login melalui tantangan OTP 6-digit (HMAC di Redis, pengiriman SMTP asinkron), login Google OAuth 2.0 / OIDC dengan PKCE backend, manajemen sesi ganda (JWT access token di memori + refresh token berputar di cookie `HttpOnly`), serta manajemen keanggotaan workspace dengan prinsip isolasi tenant.

### 1.2 Scope
**In Scope:**
- Backend Go: Modul `auth`, `users`, `workspaces` (`handler -> service -> repository`).
- Hashing password menggunakan Argon2id (`memory=64MB, iterations=3, parallelism=2`).
- Siklus tantangan OTP: generate 6 digit CSPRNG, simpan HMAC di Redis dengan TTL 5 menit, batas percobaan 5x, pengiriman asinkron via SMTP/Mailpit, anti-enumerasi akun.
- Alur Google OIDC: penanganan `state`, PKCE `code_verifier`, validasi ID token Google (JWKS, issuer, aud, nonce, `email_verified`), pembuatan sesi aplikasi, tautan akun otomatis jika email terverifikasi cocok.
- Lifecycle JWT: Access token HS256 masa berlaku 15 menit, verifikasi `tokens_valid_after` untuk pembatalan instan, refresh token opaque 256-bit di cookie `HttpOnly` (Secure, SameSite=Lax, Path=/api/v1/auth) dengan deteksi reuse family.
- Middleware Go: `AuthMiddleware`, `WorkspaceMemberMiddleware` (verifikasi peran `owner` dan penyisipan context).
- Frontend Web: Halaman login, register, verifikasi OTP, callback OAuth, pemilih workspace, dan proteksi route React Router.

**Out of Scope:**
- Alur reset password mandiri (*Password Recovery* ditandai P2 per PRD F-03).
- Model multi-peran `editor` dan `viewer` (disiapkan untuk fase berikutnya; MVP hanya mengaktifkan `owner`).
- Sistem undangan anggota workspace via email.

### 1.3 Requirements Traceability
| Requirement ID | Deskripsi Kebutuhan | Rencana Implementasi | Sumber Dokumen |
|---|---|---|---|
| F-01, F-02 | Registrasi email/password dan verifikasi OTP email | Modul auth Go, template OTP, adapter SMTP | PRD Bagian 9 & 10 |
| F-38 | Google OAuth 2.0 PKCE & OIDC backend exchange | Handler OAuth Go, verifikasi JWKS Google | PRD Bagian 11 |
| F-04, F-05 | Pembuatan & isolasi workspace multi-tenant | Modul workspace Go dan membership middleware | PRD Bagian 12 & 13 |
| SEC-AUTH-PWD | Password hashing Argon2id dan anti-enumerasi | Library `crypto/argon2` dan respons error generik | Security.md Bagian 3.1 |
| SEC-SESSION | Access token in-memory dan refresh token HttpOnly cookie | State Zustand frontend dan cookie handler Go | Security.md Bagian 3.2 |

---

## 2. Technical Context

Alur autentikasi InsightFlow AI sepenuhnya diproses oleh Go API:
- Browser mengirimkan kredensial ke `/api/v1/auth/login`. Jika valid, Go membuat `challenge_id`, menyimpan HMAC OTP di Redis, memicu goroutine pengiriman email via SMTP (Mailpit di dev), dan mengembalikan `challenge_id`.
- Klien mengirim `challenge_id` dan `otp` ke `/api/v1/auth/verify-otp`. Go memverifikasi kecocokan, menghapus challenge dari Redis, membuat hash refresh token di PostgreSQL, memasang cookie refresh `HttpOnly`, dan mengembalikan access JWT.
- Akses token JWT disimpan hanya di memori JavaScript browser (Zustand store), tidak pernah di `localStorage` atau `sessionStorage`.
- Setiap request ke resource yang dilindungi membawa header `Authorization: Bearer <jwt>`. Middleware Go memvalidasi token, memeriksa `tokens_valid_after`, mengekstrak `user_id`, dan memeriksa keanggotaan workspace pada query resource database secara atomik.

---

## 3. Implementation Tasks

### Task ID: TASK-301
**Task Name:** Password Authentication & Registration Engine with Argon2id
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-002 |
| Related Requirements | F-01, SEC-AUTH-PWD |

**Objective:**
Mengimplementasikan use-case registrasi pengguna, validasi kekuatan password, hashing Argon2id, dan mekanisme anti-enumerasi akun.

**Implementation Steps:**
1. Di `apps/api/internal/modules/auth`, buat modul hashing `argon2.go` dengan parameter: Memory=64MB, Iterations=3, Parallelism=2, Salt=16 bytes, KeyLen=32 bytes.
2. Implementasikan `POST /api/v1/auth/register`:
   - Validasi email format RFC 5322 dan panjang password 10–128 karakter.
   - Periksa ketersediaan email dengan query anti-enumerasi.
   - Buat user baru dengan status `pending_verification`.
   - Terbitkan tantangan OTP verifikasi email.
3. Implementasikan `POST /api/v1/auth/login`:
   - Verifikasi password terhadap hash Argon2id.
   - Jika kredensial salah, kembalikan respons generik `401 AUTH_INVALID_CREDENTIALS` tanpa memicu pengiriman OTP.
   - Jika kredensial benar, terbitkan tantangan OTP login.

**Expected Files:**
- `apps/api/internal/modules/auth/argon2.go`
- `apps/api/internal/modules/auth/handler.go`
- `apps/api/internal/modules/auth/service.go`
- `apps/api/internal/modules/users/repository.go`

**Technical Requirements:**
- Dilarang mencatat password plaintext atau salt mentah ke dalam log.
- Respons registrasi dan login wajib anti-enumerasi.

**Testing Requirements:**
- Unit test verifikasi password yang benar dan salah.
- Benchmark waktu hashing Argon2id (~100-300ms pada mesin dev).

**Acceptance Criteria:**
- Password tersimpan dalam database dalam format enkripsi string Argon2id yang aman.
- User terdaftar dengan benar dengan status pending.

---

### Task ID: TASK-302
**Task Name:** OTP Challenge Manager & Asynchronous Mailer Integration
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-301 |
| Related Requirements | F-02, SEC-AUTH-PWD |

**Objective:**
Membangun siklus tantangan OTP berbasis HMAC di Redis, pembatasan percobaan, throttling kirim ulang, dan pengiriman email via SMTP.

**Implementation Steps:**
1. Di `apps/api/internal/platform/mailer`, implementasikan `SMTPMailer` dan `FakeMailer` untuk keperluan testing.
2. Di modul auth, implementasikan generator OTP 6-digit acak menggunakan `crypto/rand`.
3. Simpan state di Redis dengan key `otp:{challenge_id}` memuat: HMAC-SHA256 dari kode OTP, `user_id`, `purpose` (`login` atau `register`), `attempts` (maksimal 5), dengan TTL 5 menit.
4. Kirim email OTP secara asinkron menggunakan goroutine tanpa memblokir request HTTP.
5. Implementasikan `POST /api/v1/auth/verify-otp`:
   - Periksa keberadaan challenge (Fail-closed jika Redis mati).
   - Validasi HMAC kode OTP. Jika salah, naikkan counter `attempts`. Jika melebihi 5, hapus challenge.
   - Jika benar, konsumsi challenge sekali pakai (hapus dari Redis), aktifkan status user jika registrasi, dan buat sesi JWT.
6. Implementasikan `POST /api/v1/auth/resend-otp` dengan throttling minimal jeda 60 detik antar pengiriman.

**Expected Files:**
- `apps/api/internal/platform/mailer/smtp.go`
- `apps/api/internal/modules/auth/otp.go`
- `apps/api/internal/modules/auth/otp_handler.go`

**Technical Requirements:**
- OTP mentah dilarang disimpan di database, Redis, atau dicatat dalam log.
- Redis failure pada OTP wajib menghasilkan HTTP `503 AUTH_OTP_UNAVAILABLE`.

**Testing Requirements:**
- Integration test verifikasi OTP kadaluarsa, salah kode 5x, dan konsumsi sukses menggunakan Mailpit test SMTP.

**Acceptance Criteria:**
- Email OTP terkirim ke Mailpit, tantangan tervalidasi dengan aman, dan challenge langsung terhapus setelah berhasil.

---

### Task ID: TASK-303
**Task Name:** Google OAuth 2.0 / OIDC Backend Integration with PKCE
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-301 |
| Related Requirements | F-38, SEC-AUTH-PWD |

**Objective:**
Mengimplementasikan alur login Google OAuth 2.0 / OIDC Authorization Code dengan PKCE yang aman di backend.

**Implementation Steps:**
1. Implementasikan `GET /api/v1/auth/google/login`:
   - Generate `state`, `nonce`, dan PKCE `code_verifier` (S256).
   - Simpan state di Redis dengan TTL 10 menit dan cookie pengikat browser.
   - Redirect pengguna ke URL autentikasi Google consent screen.
2. Implementasikan `GET /api/v1/auth/google/callback`:
   - Validasi parameter `state` dari request terhadap Redis dan cookie pengikat.
   - Tukarkan authorization code ke Google token endpoint menggunakan `code_verifier`.
   - Validasi ID Token OIDC: cek signature terhadap JWKS Google publik, validasi issuer (`accounts.google.com`), audience (`GOOGLE_CLIENT_ID`), expiry, nonce, dan `email_verified == true`.
   - Cari user berdasarkan `provider_subject` di `user_identities`.
   - Jika belum ada, lakukan penautan akun berdasarkan email jika email sudah terverifikasi di aplikasi, atau buat user baru.
   - Terbitkan sesi JWT aplikasi dan pasang refresh token cookie.

**Expected Files:**
- `apps/api/internal/modules/auth/google_oauth.go`
- `apps/api/internal/modules/auth/google_handler.go`

**Technical Requirements:**
- Token akses dari Google dilarang disimpan permanen di database.
- PKCE wajib menggunakan transformasi S256.

**Testing Requirements:**
- Test mocking callback Google dengan token valid dan token palsu (verifikasi penolakan signature salah).

**Acceptance Criteria:**
- Pengguna berhasil login via Google OAuth OIDC tanpa input password dan sesi aplikasi diterbitkan dengan aman.

---

### Task ID: TASK-304
**Task Name:** JWT Sessional Token Lifecycle & Revocation
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-301, TASK-302 |
| Related Requirements | F-01, SEC-SESSION |

**Objective:**
Mengelola masa berlaku access token, rotasi refresh token di PostgreSQL, dan mekanisme pencabutan token instan.

**Implementation Steps:**
1. Di `apps/api/internal/platform/token`, buat modul JWT HS256:
   - Access token memuat claim: `sub` (user_id), `exp` (15 menit), `iat`.
   - Secret key minimal 32 byte dari environment `JWT_SECRET`.
2. Implementasikan pembuatan Refresh Token:
   - Opaque random string 256-bit menggunakan `crypto/rand`.
   - Simpan hash SHA-256 dari token di tabel `refresh_tokens` dengan masa berlaku 14 hari dan `family_id`.
   - Set cookie `HttpOnly`, `Secure` (di production), `SameSite=Lax`, `Path=/api/v1/auth`.
3. Implementasikan `POST /api/v1/auth/refresh`:
   - Baca cookie refresh token, hash nilai mentah, periksa ke database.
   - Jika token sudah pernah dicabut/digunakan (indikasi reuse theft), cabut seluruh refresh token dalam `family_id` yang sama.
   - Jika valid, rotasi: buat refresh token baru, tandai token lama revoked, dan terbitkan access token baru.
4. Implementasikan `POST /api/v1/auth/logout`:
   - Cabut refresh token aktif dan hapus cookie di browser.
5. Implementasikan `AuthMiddleware`:
   - Verifikasi JWT signature dan periksa apakah `iat` lebih lama dari nilai `tokens_valid_after` pengguna.

**Expected Files:**
- `apps/api/internal/platform/token/jwt.go`
- `apps/api/internal/middleware/auth.go`
- `apps/api/internal/modules/auth/refresh_handler.go`

**Technical Requirements:**
- Access token tidak disimpan di disk browser.
- Reuse detection wajib mencabut seluruh family refresh token.

**Testing Requirements:**
- Test masa kedaluwarsa JWT, test rotasi refresh token, dan test deteksi pencurian token (family revocation).

**Acceptance Criteria:**
- Refresh token berhasil dirotasi secara mulus dan pencabutan sesi instan berfungsi saat logout.

---

### Task ID: TASK-305
**Task Name:** Workspace Creation & Membership Isolation
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-304 |
| Related Requirements | F-04, F-05, SEC-DB-ISOLATION |

**Objective:**
Membangun manajemen workspace multi-tenant dan middleware proteksi resource yang mengisolasi data per workspace.

**Implementation Steps:**
1. Implementasikan use-case workspace di `apps/api/internal/modules/workspaces`:
   - `POST /api/v1/workspaces`: Buat workspace baru dan otomatis masukkan pembuat sebagai role `owner` di `workspace_members` dalam satu transaksi DB.
   - `GET /api/v1/workspaces`: Daftar seluruh workspace di mana pengguna menjadi anggota.
   - `GET /api/v1/workspaces/:id`: Detail workspace dengan verifikasi membership.
2. Implementasikan `WorkspaceMemberMiddleware`:
   - Mengekstrak `:workspace_id` dari URL path.
   - Memeriksa keanggotaan pengguna di database (`SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`).
   - Jika tidak ditemukan atau tidak berhak, kembalikan HTTP `404 NOT_FOUND` secara seragam untuk mencegah enumerasi resource.
   - Simpan `workspace_id` dan `role` pada `gin.Context`.
3. Di frontend, buat `WorkspaceSelector` dan routing terlindungi `/app/:workspaceId/...`.

**Expected Files:**
- `apps/api/internal/modules/workspaces/handler.go`
- `apps/api/internal/modules/workspaces/service.go`
- `apps/api/internal/modules/workspaces/repository.go`
- `apps/api/internal/middleware/workspace.go`
- `apps/web/src/features/workspaces/components/WorkspaceSelector.tsx`

**Technical Requirements:**
- Akses lintas tenant wajib merespons `404` seragam, bukan `403`.
- Peran MVP terkonfirmasi adalah `owner`.

**Testing Requirements:**
- Matrix test akses: User A mencoba mengakses workspace User B dan menerima 404 Not Found.

**Acceptance Criteria:**
- Pengguna dapat membuat workspace dan data antar workspace terisolasi secara mutlak.

---

## 4. API & Database Changes

### API Changes
| Method | Endpoint | Purpose | Authentication |
|---|---|---|---|
| POST | `/api/v1/auth/register` | Pendaftaran akun baru | None |
| POST | `/api/v1/auth/login` | Login email & password (memicu OTP) | None |
| POST | `/api/v1/auth/verify-otp` | Verifikasi tantangan OTP & penerbitan sesi | None |
| POST | `/api/v1/auth/resend-otp` | Kirim ulang OTP dengan throttling | None |
| GET | `/api/v1/auth/google/login` | Inisiasi alur Google OAuth PKCE | None |
| GET | `/api/v1/auth/google/callback` | Callback penukaran authorization code | None |
| POST | `/api/v1/auth/refresh` | Rotasi refresh token cookie | Cookie HttpOnly |
| POST | `/api/v1/auth/logout` | Pencabutan sesi & penghapusan cookie | Bearer JWT |
| GET | `/api/v1/auth/me` | Profil pengguna aktif | Bearer JWT |
| POST | `/api/v1/workspaces` | Pembuatan workspace baru | Bearer JWT |
| GET | `/api/v1/workspaces` | Daftar workspace pengguna | Bearer JWT |
| GET | `/api/v1/workspaces/:id` | Detail workspace aktif | Bearer JWT + Member |

### Database Changes
Menggunakan tabel yang telah disiapkan pada Plan 02: `users`, `refresh_tokens`, `user_identities`, `workspaces`, `workspace_members`. Tidak ada perubahan DDL baru.

---

## 5. Design & UI Implementation

- **Related Pages:**
  - `/login`: Form email/password dengan link pendaftaran dan tombol login Google OAuth.
  - `/register`: Form nama lengkap, email, password dengan indikator kekuatan password.
  - `/verify-otp`: Form input 6-digit OTP dengan countdown timer 60 detik untuk kirim ulang.
  - `/app/:workspaceId/overview`: Halaman dashboard awal setelah memilih workspace.
- **Component Requirements:**
  - `Input`, `Button`, `Card`, `Badge` dari shadcn/ui.
  - `OTPInput` 6 kotak angka otomatis pindah fokus saat diketik.
  - `WorkspaceSelector` dropdown pada header AppShell.
- **Design Tokens:**
  - Gunakan `--primary` (Indigo), `--background`, `--surface`, `--danger` untuk pesan error inline.
- **Accessibility Requirements:**
  - Pesan error form terhubung via `aria-describedby` ke input terkait.
  - Target sentuh tombol minimal 40×40 px untuk mobile.

---

## 6. Security Requirements

- Validasi anti-enumerasi: Login gagal selalu mengembalikan `AUTH_INVALID_CREDENTIALS` tanpa membedakan email tidak terdaftar atau password salah.
- Rate limiting auth: Maksimal 5 percobaan login/menit per kombinasi IP + Email via Redis (mengembalikan HTTP 429).
- Proteksi CSRF: Endpoint `/auth/refresh` dan `/auth/logout` memeriksa header `X-Requested-With: XMLHttpRequest` dan origin allowlist.
- Token Isolation: Refresh token hanya dikirim ke path `/api/v1/auth` dan dilindungi flag `HttpOnly`.

---

## 7. Testing & Verification

- **Unit Tests:** Hashing Argon2id, parsing JWT claims, validasi password generator OTP.
- **Integration Tests:**
  - Skenario registrasi -> verifikasi OTP via Mailpit -> login berhasil.
  - Skenario proteksi workspace: User 1 membuat resource, User 2 mencoba akses via API -> response 404.
  - Skenario family refresh token revocation: refresh token digunakan dua kali -> seluruh sesi dibatalkan.
- **Verification Commands:**
  - `cd apps/api && go test -v ./internal/modules/auth/...`
  - `cd apps/api && go test -v ./internal/modules/workspaces/...`
  - `cd apps/web && npm test`

---

## 8. Commit Plan

### 8.1 Commit Strategy
Memisahkan fungsionalitas identitas: hashing & model pengguna -> tantangan OTP & mailer -> Google OAuth PKCE -> session management & middleware -> modul workspace & frontend UI.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-301 | feat | auth | feat(auth): implement user registration and Argon2id password authentication |
| 02 | TASK-302 | feat | auth | feat(auth): add Redis-backed OTP challenge engine and async email dispatch |
| 03 | TASK-303 | feat | auth | feat(auth): implement Google OAuth 2.0 and OIDC login with backend PKCE |
| 04 | TASK-304 | feat | auth | feat(auth): implement JWT access and rotating refresh token cookie lifecycle |
| 05 | TASK-305 | feat | workspaces | feat(workspaces): implement workspace domain, tenant isolation, and RBAC middleware |
| 06 | TASK-301..305 | feat | web | feat(web): implement auth forms, OTP verification, and workspace UI routes |

### 8.3 Commit Details
- **Commit 01:** Menyertakan modul Argon2id, handler registrasi/login, dan repositori user.
- **Commit 02:** Menyertakan modul OTP, adapter SMTP, template email, dan verifikasi challenge.
- **Commit 03:** Menyertakan endpoint callback Google, validasi JWKS OIDC, dan penautan akun.
- **Commit 04:** Menyertakan generator token HS256, repositori refresh token, middleware auth, dan handler refresh/logout.
- **Commit 05:** Menyertakan handler workspace, repository workspace membership, dan middleware isolasi tenant.
- **Commit 06:** Menyertakan halaman React untuk login, register, OTP input, dan routing workspace di frontend.

### 8.4 Commit Boundaries
- Logika backend identitas harus selesai dan teruji sebelum komponen UI frontend diintegrasikan.

### 8.5 Commit Dependencies
- Commit 02 bergantung pada 01. Commit 04 bergantung pada 01 & 02. Commit 05 bergantung pada 04. Commit 06 bergantung pada seluruh commit backend.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Delay pengiriman email OTP dari penyedia SMTP | Pengguna tertahan di layar verifikasi | Sedang | Tampilkan countdown resend 60 detik yang jelas dan gunakan Mailpit untuk verifikasi instan di dev. |
| Redis crash saat tantangan OTP aktif | Tantangan OTP hilang | Sedang | Terapkan kebijakan Fail-Closed: kembalikan HTTP 503 dan minta pengguna login ulang setelah Redis pulih. |

---

## 10. Definition of Done

- [ ] Registrasi pengguna baru berhasil dengan hashing Argon2id.
- [ ] Kode OTP 6-digit terkirim ke Mailpit lokal dan berhasil diverifikasi.
- [ ] Login Google OAuth PKCE berfungsi dan memvalidasi ID token OIDC.
- [ ] Access token tersimpan hanya di memori dan refresh token dirotasi di cookie `HttpOnly`.
- [ ] Pembuatan workspace berhasil dan otorisasi membership memblokir akses lintas tenant dengan respons 404.
- [ ] Seluruh unit dan integration test auth lulus 100%.
