# Plan 10: Job Queue Wake-up Signal (Redis, Fail-Open)

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-010 |
| Name | Redis Wake-up Signal for the PostgreSQL Job Worker |
| Priority | P2 – Medium |
| Complexity | Small |
| Status | Planned |
| Depends On | PLAN-002, PLAN-004 |
| Related Requirements | F-08, F-09 (latensi & beban antrean job) |
| Additive To | Plan 04 TASK-403 |

### 1.1 Objective

Menurunkan latensi pengambilan job dari rata-rata ~1 detik (interval polling 2 detik) menjadi milidetik, dan menghapus query `UPDATE ... SKIP LOCKED` saat antrean kosong, tanpa memindahkan antrean keluar dari PostgreSQL.

Redis hanya menjadi **sinyal bangun best-effort**. `processing_jobs` tetap satu-satunya antrean dan sumber kebenaran. Jika Redis gagal, worker kembali ke polling seperti sekarang (**fail-open**).

### 1.2 Scope

**In Scope:**
- Primitif sinyal di `platform/cache`: kirim sinyal (`LPUSH` + `LTRIM` + `EXPIRE`) dan tunggu sinyal (`BRPOP` dengan timeout).
- Interface kecil di sisi consumer: `datasets.Notifier` untuk mengirim, `jobs.Waiter` untuk menunggu.
- Sinyal dikirim **setelah** transaksi dataset/version/job commit; kegagalan sinyal hanya di-log.
- Worker menunggu sinyal dengan timeout `idleInterval` sebagai jaring pengaman.
- Test: sinyal terkirim sekali saat sukses, nol saat rollback, worker tetap memproses saat Redis mati.

**Out of Scope:**
- Memindahkan antrean ke Redis (Opsi B/asynq), outbox pattern, atau menghapus tabel `processing_jobs`.
- Perubahan skema database, endpoint API, status dataset, atau logika retry/backoff.
- Sinyal untuk retry terjadwal (`run_after` di masa depan) — retry tetap mengandalkan polling.
- Metrik Prometheus; observabilitas MVP cukup dari log `slog`.

### 1.3 Requirements Traceability

| Requirement ID | Deskripsi | Rencana Implementasi | Sumber |
|---|---|---|---|
| F-08, F-09 | Worker mengambil job secara aman dan cepat | `ClaimNextJob` tetap; tambah `BRPOP` sebagai pemicu | PRD Bagian 16 & 17 |
| ADR-0011/0014 | Antrean job MVP memakai PostgreSQL, bukan broker | Redis hanya sinyal; job tetap di DB | PRD ADR-0011, ADR-0014 |
| SEC-REDIS | Redis soft state ber-TTL, bukan sumber kebenaran | Key sinyal TTL 60 detik, tanpa payload | Security.md |

---

## 2. Technical Context

Alur sekarang:

```
upload → COMMIT dataset+version+job → 202
worker → UPDATE ... SKIP LOCKED → kosong? tidur 2s → ulangi
```

Alur setelah Plan 10:

```
upload → COMMIT dataset+version+job → LPUSH if:{env}:jobs:wake → 202
worker → UPDATE ... SKIP LOCKED → kosong? BRPOP (timeout 2s)
           ↑ sinyal datang ─────────────┘
```

Properti yang dipertahankan:
- `dataset + version + job` tetap satu transaksi Postgres.
- Klaim job tetap `FOR UPDATE SKIP LOCKED`; dua worker tidak mungkin mengambil job sama.
- `attempts`, `max_attempts`, `run_after`, status, dan endpoint `/datasets/:id/status` tidak berubah.

### 2.1 Keputusan desain

| Keputusan | Alasan |
|---|---|
| `LIST` + `BRPOP`, bukan Pub/Sub | Sinyal tidak hilang jika worker sedang sibuk; timeout tetap jadi jaring |
| Nilai sinyal `"1"`, tanpa payload | Tidak ada `dataset_id`/`workspace_id` di Redis; tidak ada state kedua yang bisa divergen |
| TTL 60 detik + `LTRIM` 4×worker | Token basi tidak menumpuk; key tetap memenuhi aturan "Redis ber-TTL" |
| Sinyal setelah commit, best-effort | Atomicity tidak berubah; gagal sinyal = fallback polling, bukan job hilang |
| Tidak ada env var baru | Sinyal aktif otomatis bila klien Redis tersedia; `nil` menonaktifkan |
| Interface di sisi consumer | Menghindari import cycle `datasets → jobs`; sesuai Architecture.md §4.2 |

### 2.2 Kontrak interface

```go
// modules/datasets/service.go (consumer side)
type Notifier interface {
    Signal(ctx context.Context, key string) error
}

// modules/jobs/worker.go (consumer side)
type Waiter interface {
    Wait(ctx context.Context, key string, timeout time.Duration)
}
```

Satu adapter `platform/cache.WakeSignal` memenuhi keduanya. `datasets` tidak mengimpor `jobs`, dan sebaliknya hanya `jobs → datasets` seperti sekarang.

---

## 3. Implementation Tasks

### Task ID: TASK-1001
**Task Name:** Redis Wake-up Signal Primitives
| Field | Description |
|---|---|
| Priority | P2 |
| Complexity | Small |
| Status | Planned |
| Dependencies | PLAN-002 |
| Related Requirements | F-08, F-09, SEC-REDIS |

**Objective:**
Menambahkan primitif sinyal job di `platform/cache` tanpa mengubah `CacheClient` yang ada.

**Implementation Steps:**
1. Buat `apps/api/internal/platform/cache/wake.go`.
2. Konstanta `JobWakeKey = build_key(env, "jobs", "wake")` dan `wakeTTL = 60 * time.Second`, `wakeCap = 4 * workers`.
3. `Signal(ctx, key)`: pipeline `LPUSH key "1"`, `LTRIM key 0 wakeCap-1`, `EXPIRE key wakeTTL`.
4. `Wait(ctx, key, timeout)`: `BRPOP ctx timeout key`; kembalikan tanpa error ke pemanggil (timeout bukan kegagalan).
5. Gunakan klien Redis terpisah untuk operasi blocking agar `BRPOP` tidak menghabiskan pool cache biasa; `PoolSize` minimal `workers+1`.
6. Semua error dikembalikan ke pemanggil; pemanggil yang memutuskan fail-open.

**Expected Files:**
- `apps/api/internal/platform/cache/wake.go`
- `apps/api/internal/platform/cache/wake_test.go`

**Technical Requirements:**
- Perintah hanya `LPUSH`, `LTRIM`, `EXPIRE`, `BRPOP` — perintah dasar yang juga didukung Memurai.
- Tidak ada nilai rahasia, `dataset_id`, atau isi dataset di Redis.
- Key selalu TTL; tidak ada key permanen.

**Testing Requirements:**
- Test dengan Redis asli (`TEST_REDIS_ADDR`, skip bila kosong): `Signal` lalu `Wait` mengembalikan < timeout.
- Test `LTRIM` membatasi panjang list.
- Test TTL key > 0.
- Test `Wait` menghormati pembatalan context.

**Acceptance Criteria:**
- Sinyal terkirim dan diterima; key memiliki TTL; panjang list tidak melebihi batas.

---

### Task ID: TASK-1002
**Task Name:** Best-Effort Signal After Dataset Commit
| Field | Description |
|---|---|
| Priority | P2 |
| Complexity | Small |
| Status | Planned |
| Dependencies | TASK-1001 |
| Related Requirements | F-07, F-08 |

**Objective:**
Membangunkan worker segera setelah dataset/version/job commit, tanpa mengubah atomicity.

**Implementation Steps:**
1. Tambah field `notifier Notifier` pada `datasets.Service` + opsi konstruktor (functional option atau parameter eksplisit).
2. Di `Service.Create`, setelah `txm.WithinTx` sukses, panggil `notifier.Signal(ctx, cache.JobWakeKey)`.
3. Abaikan error sinyal; log `slog.Warn` tanpa `dataset_id` mentah bila gagal.
4. Jangan kirim sinyal bila transaksi rollback atau bila `notifier == nil`.
5. Jangan kirim sinyal pada `RequeueJob` (retry memakai `run_after` + polling).

**Expected Files:**
- `apps/api/internal/modules/datasets/service.go`
- `apps/api/internal/modules/datasets/service_test.go`
- `apps/api/cmd/api/main.go` (wiring adapter)

**Technical Requirements:**
- Sinyal **setelah** commit; tidak boleh di dalam transaksi.
- Kegagalan sinyal tidak boleh menggagalkan upload (tetap `202`).
- Tidak ada import `jobs` dari `datasets`.

**Testing Requirements:**
- Fake notifier: `Create` sukses → `Signal` dipanggil tepat sekali.
- Fake notifier: `Create` gagal/rollback → `Signal` tidak dipanggil.
- Fake notifier mengembalikan error → `Create` tetap sukses dan job tetap `queued`.

**Acceptance Criteria:**
- Upload tetap 202 walau Redis mati; job tetap diproses lewat polling.

---

### Task ID: TASK-1003
**Task Name:** Worker Waits on Signal with Polling Fallback
| Field | Description |
|---|---|
| Priority | P2 |
| Complexity | Small |
| Status | Planned |
| Dependencies | TASK-1001, TASK-1002 |
| Related Requirements | F-08 |

**Objective:**
Mengganti tidur tetap di worker dengan `BRPOP` ber-timeout, tetap memproses lewat DB bila sinyal tidak datang.

**Implementation Steps:**
1. Tambah field `waiter Waiter` pada `jobs.WorkerPool`.
2. Di `loop`, setelah `ProcessOne` mengembalikan `worked == false`:
   - jika `waiter != nil` → `waiter.Wait(ctx, key, idleInterval)`
   - jika tidak → perilaku sekarang (`time.After`)
3. Tetap cek `ctx.Err()` setiap iterasi agar graceful shutdown.
4. Jangan mengubah `Dispatcher`, `Repository`, atau query klaim.

**Expected Files:**
- `apps/api/internal/modules/jobs/worker.go`
- `apps/api/internal/modules/jobs/worker_test.go`
- `apps/api/cmd/api/main.go`

**Technical Requirements:**
- `Wait` tidak boleh memblokir shutdown lebih dari `idleInterval`.
- Sinyal berlebih (lebih banyak dari job) hanya menyebabkan satu iterasi kosong; tidak boleh busy-loop.
- Perilaku tanpa Redis identik dengan implementasi sekarang.

**Testing Requirements:**
- Fake waiter yang langsung kembali → `ProcessOne` dipanggil lagi.
- Fake waiter yang timeout → loop tetap berjalan dan shutdown via context.
- Test regresi: seluruh `dispatcher_test.go` lama tetap lulus tanpa perubahan.

**Acceptance Criteria:**
- Worker bangun < 200 ms setelah sinyal; tetap memproses ≤ 2 detik setelah Redis dimatikan.

---

### Task ID: TASK-1004
**Task Name:** Documentation and Failure-Mode Verification
| Field | Description |
|---|---|
| Priority | P2 |
| Complexity | Small |
| Status | Planned |
| Dependencies | TASK-1001..1003 |

**Objective:**
Menyelaraskan dokumentasi arsitektur/keamanan dan memverifikasi kebijakan fail-open.

**Implementation Steps:**
1. Perbarui `docs/core_docs/Architecture.md` (job queue, Redis, retry/fail policy, decision table).
2. Perbarui `docs/core_docs/Security.md` (key sinyal, TTL, tanpa payload, fail-open).
3. Perbarui `AGENTS.md` (pengecualian TTL untuk key sinyal).
4. Perbarui `planning/04` (addendum), `planning/00-roadmap.md`, `planning/README.md`, `planning/09` (verifikasi E2E).
5. Catat bahwa PRD tidak berubah: antrean tetap PostgreSQL (ADR-0011/0014).

**Expected Files:**
- `docs/core_docs/Architecture.md`
- `docs/core_docs/Security.md`
- `AGENTS.md`
- `planning/04-dataset-pipeline-profiling.md`
- `planning/09-e2e-testing-production-readiness.md`
- `planning/00-roadmap.md`
- `planning/README.md`

**Acceptance Criteria:**
- Tidak ada dokumen yang menyebut Redis sebagai antrean job.
- Kebijakan fail-open sinyal tertulis dan diuji.

---

## 4. API & Database Changes

### API Changes
Tidak ada. Kontrak upload, detail, dan status dataset tetap sama.

### Database Changes
Tidak ada. Skema `processing_jobs` tidak berubah; tidak ada migration baru.

### Configuration Changes
Tidak ada env var baru. Sinyal memakai `REDIS_URL` yang sudah ada. Menonaktifkan sinyal cukup dengan tidak meng-inject adapter.

---

## 5. Design & UI Implementation

Tidak ada perubahan UI. Manfaat yang terlihat pengguna: badge `processing` muncul lebih cepat setelah upload.

---

## 6. Security Requirements

- Key `if:{env}:jobs:wake` selalu ber-TTL (≤ 60 detik) dan hanya berisi `"1"`.
- Tidak ada `dataset_id`, `workspace_id`, token, atau isi dataset di Redis.
- Redis tetap bind privat/loopback; ACL dan larangan perintah berbahaya tidak berubah.
- Kegagalan Redis **fail-open**: upload tetap diterima, job tetap diproses lewat polling.
- Sinyal tidak boleh dipakai untuk otorisasi atau penentuan status; status tetap dibaca dari PostgreSQL.

---

## 7. Testing & Verification

- **Unit/Integration (Go):**
  - `cd apps/api; go build ./...; go vet ./...`
  - `cd apps/api; go test ./internal/modules/datasets/... ./internal/modules/jobs/... ./internal/platform/cache/...`
- **Dengan DB & Redis test:**
  - `TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/insightflow_test?sslmode=disable`
  - `TEST_REDIS_ADDR=127.0.0.1:6379`
- **Skenario wajib:**
  1. Upload → job diproses < 200 ms saat worker idle.
  2. Redis dimatikan → upload tetap 202, job diproses ≤ 2 detik (polling).
  3. Dua worker, satu sinyal → tidak ada klaim ganda.
  4. 50 sinyal tanpa job → tidak ada busy-loop; panjang list ≤ batas.
  5. Shutdown saat `BRPOP` → worker berhenti ≤ `idleInterval`.
- **Regresi:** seluruh test Plan 04 lulus tanpa perubahan perilaku.

---

## 8. Commit Plan

### 8.1 Commit Strategy
Memisahkan primitif Redis, integrasi pengirim sinyal, integrasi worker, lalu test dan dokumentasi.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-1001 | feat | cache | feat(cache): add TTL-bounded job wake-up signal primitives |
| 02 | TASK-1002 | feat | datasets | feat(datasets): signal job worker after dataset commit |
| 03 | TASK-1003 | feat | jobs | feat(jobs): wait on wake-up signal with polling fallback |
| 04 | TASK-1001..1003 | test | jobs | test(jobs): cover wake signal, fail-open, and idle behavior |
| 05 | TASK-1004 | docs | architecture | docs(architecture): document Redis wake-up signal as fail-open optimization |

### 8.3 Commit Details
- **Commit 01:** `wake.go` + test primitif (`LPUSH`/`LTRIM`/`EXPIRE`/`BRPOP`).
- **Commit 02:** `Notifier` di `datasets`, sinyal setelah commit, fake notifier test, wiring `main.go`.
- **Commit 03:** `Waiter` di `jobs/worker.go`, ganti `time.After`, wiring.
- **Commit 04:** test fail-open, batas token, dua worker, shutdown.
- **Commit 05:** Architecture/Security/AGENTS/planning.

### 8.4 Commit Boundaries
- Commit 02 dan 03 tidak boleh mengubah skema DB atau query `ClaimNextJob`.
- Commit 05 dijalankan setelah perilaku fail-open terverifikasi.

### 8.5 Commit Dependencies
- 02 bergantung pada 01. 03 bergantung pada 01. 04 bergantung pada 02 dan 03. 05 terakhir.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Redis menjadi titik gagal baru di jalur upload | Upload gagal bila sinyal dianggap wajib | Rendah | Sinyal best-effort; error diabaikan; job tetap di Postgres |
| Token sinyal menumpuk | Memori Redis naik, worker bangun sia-sia | Sedang | `LTRIM` 4×worker + TTL 60 detik |
| `BRPOP` menghabiskan koneksi pool | Cache lain ikut terblokir | Sedang | Klien blocking terpisah; `PoolSize ≥ workers+1` |
| Sinyal hilang saat worker sibuk | Job menunggu lebih lama | Rendah | Timeout polling 2 detik tetap ada |
| Perilaku Memurai berbeda dari Redis | Dev dan produksi tidak sama | Rendah | Hanya perintah dasar; test CI Linux terhadap Redis resmi |
| Apakah sinyal harus aktif default? | Kompleksitas konfigurasi | Rendah | Aktif otomatis bila Redis tersedia; `nil` untuk menonaktifkan; tanpa env var |

---

## 10. Definition of Done

- [ ] Upload → worker mulai memproses < 200 ms saat idle (diukur lokal).
- [ ] Saat antrean kosong, tidak ada query klaim job ke PostgreSQL.
- [ ] Redis dimatikan → upload tetap `202` dan job diproses ≤ 2 detik.
- [ ] Tidak ada job hilang, tidak ada klaim ganda, `attempts`/`run_after` tetap benar.
- [ ] Key sinyal ber-TTL, tanpa payload sensitif, panjang list terbatas.
- [ ] Seluruh test Plan 04 tetap lulus tanpa perubahan.
- [ ] `go build`, `go vet`, dan test modul terkait lulus.
- [ ] Architecture/Security/AGENTS/planning diperbarui; PRD tidak berubah.
