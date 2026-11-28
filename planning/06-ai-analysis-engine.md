# Plan 06: Deterministic AI Analysis Loop & SSE Streaming Gateway

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-006 |
| Name | Structured AI Planner, Deterministic Tools, Grounding & SSE Streaming |
| Priority | P0 – Critical |
| Complexity | Large |
| Status | Planned |
| Dependencies | PLAN-004 |
| Related Requirements | F-14, F-15 (Sessions & SSE), F-16, F-17 (Planner & Tools), F-18, F-19 (Narrator & Grounding) |

### 1.1 Objective
Membangun inti analitik berbasis AI yang dapat dipertanggungjawabkan: orkestrasi sesi percakapan di Go API, relay streaming Server-Sent Events (SSE), perencana terstruktur LLM (*AnalysisPlan*), eksekutor tool analitik deterministik berbasis DuckDB terhadap file Parquet, validasi hasil (*ResultEnvelope*), narator ringkasan dengan *Grounding Checker* anti-halusinasi, serta penentuan spesifikasi visualisasi (*ChartSpec*).

### 1.2 Scope
**In Scope:**
- Sesi Analisis di Go API: endpoint `POST /api/v1/analysis/sessions` dan pengiriman pesan streaming `POST /api/v1/analysis/sessions/:id/messages` (SSE via `fetch` + `ReadableStream`).
- Penguncian analisis (*run lock*) di Redis untuk mencegah request konkuren ganda pada satu sesi.
- Context Builder di Go: menyusun konteks skema kolom, ringkasan statistik, dan riwayat pesan terbatas untuk dikirim ke Engine.
- AI Orchestrator di Python Engine:
  - Planner: memanggil LLM (OpenAI-compatible) untuk menghasilkan rencana analisis JSON terstruktur (`AnalysisPlan`).
  - Plan Validator: memeriksa skema kolom, batas argumen, dan allowlist tool (menolak eksekusi kode bebas atau SQL dari LLM).
  - Tool Registry: registry statis tool deterministik DuckDB (`aggregate_metric`, `group_by_metric`, `time_series_metric`, `correlation_matrix`, `distribution_metric`).
  - Cache Deterministic: cek Redis untuk hasil tool yang identik sebelum komputasi (`rc:{tool_hash}`).
  - Result Envelope Validator: memvalidasi hasil kalkulasi numerik terhadap invarian data.
  - Narrator: mengirimkan hasil agregat terbatas (bukan dataset mentah) ke LLM untuk penyusunan narasi bisnis.
  - Grounding Checker: memverifikasi bahwa setiap angka numerik dalam narasi teks benar-benar ada dalam `ResultEnvelope`.
  - Chart Spec Generator: aturan deterministik yang memetakan hasil tool ke spesifikasi chart (`bar`, `line`, `scatter`, `pie`).
- SSE Streaming Event Relay: streaming event tahapan (`stage_start`, `plan_generated`, `tool_executed`, `narrative_chunk`, `chart_ready`, `run_completed`).
- Frontend Web: Antarmuka chat AI responsif dengan indikator tahap pengerjaan (*StageIndicator*), panel rencana analisis (*PlanPanel*), dan streaming pesan.

**Out of Scope:**
- Penggunaan library autonomous agent bebas yang tidak terkendali (seperti LangChain agent atau AutoGPT).
- Eksekusi kode Python bebas (dilarang `eval`, `exec`, atau shell script).
- Pemanggilan API LLM eksternal secara langsung dari browser atau Go API (hanya Engine yang memiliki akses ke provider LLM).

### 1.3 Requirements Traceability
| Requirement ID | Deskripsi Kebutuhan | Rencana Implementasi | Sumber Dokumen |
|---|---|---|---|
| F-14, F-15 | Sesi analisis, context builder, dan streaming SSE | Modul analysis Go & SSE client web | PRD Bagian 22 & 23 |
| F-16, F-17 | Planner terstruktur & eksekusi tool DuckDB deterministik | Orchestrator Engine & Registry tool | PRD Bagian 24 & 25 |
| F-18, F-19 | Narrator bisnis, grounding checker, & ChartSpec | Grounding validator & Chart rule engine | PRD Bagian 26 & 27 |
| SEC-LLM-DATA | Data minimisation ke LLM (tidak mengirim baris mentah) | Context reducer Engine | Security.md Bagian 4.3 |

---

## 2. Technical Context

Alur lengkap siklus AI Analysis:
1. Browser mengirimkan pesan pertanyaan ke Go API via SSE: `POST /api/v1/analysis/sessions/:id/messages`.
2. Go API memverifikasi keanggotaan workspace, memastikan versi dataset berstatus `ready`, memeriksa kuota harian run analisis di Redis, dan memasang lock run sesi di Redis (`lock:analysis:{session_id}`).
3. Go API menyimpan pesan pengguna di `analysis_messages`, membuat record `analysis_runs` (`status = running`), dan membangun konteks skema kolom & profil.
4. Go API melakukan request streaming HTTP internal ke Python Engine `POST /v1/ai/analyze` dengan header `X-Internal-Token` dan `X-Request-ID`.
5. **Engine - Tahap 1 (Planning):** Engine memanggil LLM provider dengan prompt perencana sistem yang berversi. LLM mengembalikan JSON `AnalysisPlan`.
6. **Engine - Tahap 2 (Validasi Plan):** `PlanValidator` memverifikasi struktur schema Pydantic (`extra=forbid`), memastikan nama kolom valid pada skema Parquet, dan memeriksa batas parameter.
7. **Engine - Tahap 3 (Eksekusi Deterministik):** Engine memeriksa cache hasil Redis. Jika cache miss, tool terdaftar menyusun query DuckDB terparameter terhadap file Parquet lokal read-only. Hasil dibungkus dalam `ResultEnvelope`. Invarian divalidasi dan disimpan di cache Redis (TTL 24 jam).
8. **Engine - Tahap 4 (Narasi & Grounding):** Hasil ringkasan agregat (<50 baris) dikirim ke LLM Narrator. Narasi teks yang dihasilkan diperiksa oleh `GroundingChecker`: semua angka diekstrak dan dicocokkan dengan angka di `ResultEnvelope`. Jika angka tidak cocok (halusinasi), narasi diulang sekali atau diganti dengan template deterministik.
9. **Engine - Tahap 5 (Visualisasi):** Aturan deterministik memilih tipe chart yang valid dan menghasilkan `ChartSpec`.
10. Engine men-stream event ke Go API; Go API meneruskannya secara real-time ke browser pengguna melalui SSE stream.
11. Setelah stream selesai, Go API menyimpan jawaban asisten di `analysis_messages`, hasil tool dan chart di `analysis_results`, konsumsi token di `usage_records`, dan menandai run `completed` di PostgreSQL. Lock sesi di Redis dilepas.

---

## 3. Implementation Tasks

### Task ID: TASK-601
**Task Name:** Analysis Session Manager & Context Builder in Go API
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-003, PLAN-004 |
| Related Requirements | F-14, SEC-LLM-DATA |

**Objective:**
Mengimplementasikan modul sesi analisis di Go API untuk mengelola percakapan, menyimpan riwayat pesan, dan menyusun context prompt yang aman.

**Implementation Steps:**
1. Di `apps/api/internal/modules/analysis`, buat `handler.go`, `service.go`, dan `repository.go`.
2. Implementasikan `POST /api/v1/analysis/sessions`: membuat sesi baru untuk dataset tertentu.
3. Implementasikan `GET /api/v1/analysis/sessions`: daftar riwayat sesi dalam workspace.
4. Implementasikan `GET /api/v1/analysis/sessions/:id`: detail sesi beserta riwayat pesan dan hasil run sebelumnya.
5. Buat modul `context_builder.go`:
   - Membaca skema kolom dari `dataset_columns` (nama kolom, tipe data).
   - Membaca statistik ringkas dari `dataset_profiles` (jumlah baris, min, max).
   - Mengambil 5 interaksi pesan terakhir untuk menjaga batas context window.
   - Mengemas payload konteks ke format JSON terstandarisasi untuk Engine.

**Expected Files:**
- `apps/api/internal/modules/analysis/handler.go`
- `apps/api/internal/modules/analysis/service.go`
- `apps/api/internal/modules/analysis/context_builder.go`

**Technical Requirements:**
- Context builder dilarang menyertakan baris dataset mentah atau data sensitif pengguna.

**Testing Requirements:**
- Unit test context builder memverifikasi format konteks skema dan pemangkasan riwayat pesan.

**Acceptance Criteria:**
- Sesi percakapan berhasil dibuat, riwayat pesan tersimpan di PostgreSQL, dan konteks terkompilasi bersih.

---

### Task ID: TASK-602
**Task Name:** SSE Streaming Gateway & Session Concurrency Lock
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-601 |
| Related Requirements | F-15, SEC-SESSION |

**Objective:**
Mengimplementasikan endpoint streaming SSE di Go API dengan penguncian konkurensi berbasis Redis untuk merelay tahapan analisis dari Engine ke browser.

**Implementation Steps:**
1. Implementasikan `POST /api/v1/analysis/sessions/:id/messages`:
   - Pasang header response SSE: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`.
   - Gunakan lock Redis dengan key `lock:analysis:{session_id}` (TTL 3 menit) untuk mencegah submit pesan ganda saat proses berjalan.
   - Simpan pesan pengguna ke tabel `analysis_messages`.
   - Buka stream HTTP POST ke Engine `/v1/ai/analyze`.
   - Baca stream chunk event dari Engine dan relay langsung ke `gin.Context.Writer` dengan `Flush()`.
   - Tangani pemutusan koneksi klien (klien menutup browser / `AbortController`) dengan membatalkan context request ke Engine.
2. Saat stream selesai atau error, perbarui status `analysis_runs` di database dan hapus lock Redis.

**Expected Files:**
- `apps/api/internal/modules/analysis/sse_handler.go`
- `apps/api/internal/modules/analysis/sse_relay.go`

**Technical Requirements:**
- Request SSE wajib membawa JWT Bearer yang valid.
- Buffer stream tidak boleh menahan event secara berlebihan.

**Testing Requirements:**
- Integration test pengiriman event SSE berurutan dan verifikasi pelepasan lock Redis.

**Acceptance Criteria:**
- Klien menerima aliran event SSE (`stage_start`, `plan_generated`, `run_completed`) secara real-time.

---

### Task ID: TASK-603
**Task Name:** LLM Planner & Plan Semantic Validator
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-001 |
| Related Requirements | F-16, SEC-LLM-DATA |

**Objective:**
Membangun modul perencana di Python Engine yang memetakan pertanyaan bahasa alami ke dalam rencana JSON `AnalysisPlan` dan memvalidasi keamanannya.

**Implementation Steps:**
1. Di `apps/engine/app/orchestration`, buat modul `planner.py`.
2. Siapkan prompt sistem terstandarisasi di `app/llm/prompts/planner/v1.txt` dengan instruksi ketat untuk menghasilkan JSON `AnalysisPlan`.
3. Definisikan model Pydantic di `app/schemas/plan.py`:
   - `AnalysisStep`: `tool` (string dari allowlist), `args` (dict parameter), `intent` (penjelasan langkah).
   - `AnalysisPlan`: `question_classification`, `steps` (maksimal 3 langkah berurutan), `requires_clarification` (boolean).
   - Atur konfigurasi model `extra = "forbid"`.
4. Buat modul `plan_validator.py`:
   - Verifikasi bahwa tool yang diminta ada dalam registry tool terdaftar.
   - Verifikasi bahwa setiap kolom yang dirujuk dalam argumen tool benar-benar ada dalam skema Parquet.
   - Tolak rencana yang meminta eksekusi kode bebas atau query di luar allowlist.
   - Verifikasi **kecocokan tipe**, bukan hanya nama: `sum`/`avg` hanya pada kolom numerik; `time_series_metric.group_by` wajib bertipe `date` dan `bucket` wajib terisi.
   - Perlakukan identifier sebagai string yang harus **sama persis** dengan nama kolom schema; tolak identifier yang memuat spasi, tanda kurung, atau ekspresi SQL (mis. `date_trunc('month', created)`).
   - Jika plan invalid, picu satu kali percobaan perbaikan (repair prompt) ke LLM.
5. Panggil LLM dengan `response_format: {"type": "json_schema", "strict": true}` yang memuat aturan kondisional (`allOf` + `if/then`: `time_series_metric` → `required: [group_by, bucket]`). Jangan menyediakan jalur `json_object` (lihat §9.1).

**Expected Files:**
- `apps/engine/app/orchestration/planner.py`
- `apps/engine/app/orchestration/plan_validator.py`
- `apps/engine/app/schemas/plan.py`
- `apps/engine/app/llm/client.py`

**Technical Requirements:**
- Menggunakan OpenAI-compatible API client dengan timeout 30 detik.
- Dilarang mempercayai output LLM tanpa validasi skema runtime.

**Testing Requirements:**
- Unit test validator dengan plan yang memuat kolom fiktif dan tool palsu untuk memastikan penolakan error.

**Acceptance Criteria:**
- Pertanyaan pengguna berhasil diterjemahkan menjadi rencana terstruktur yang valid secara semantik.

---

### Task ID: TASK-604
**Task Name:** Deterministic Analytics Tool Registry & Result Validator
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Large |
| Status | Planned |
| Dependencies | TASK-603, PLAN-004 |
| Related Requirements | F-17, SEC-PARAM-SQL |

**Objective:**
Mengimplementasikan registry tool analitik deterministik berbasis DuckDB terhadap file Parquet dan validasi hasil kalkulasi numerik.

**Implementation Steps:**
1. Di `apps/engine/app/tools`, buat `registry.py` dengan tool terdaftar:
   - `aggregate_metric`: menghitung `sum`, `avg`, `min`, `max`, `count` pada kolom numerik dengan filter opsional.
   - `group_by_metric`: agregasi metrik dikelompokkan berdasarkan kolom kategori (maksimal 50 grup teratas).
   - `time_series_metric`: agregasi metrik berdasarkan interval waktu (harian, mingguan, bulanan).
   - `distribution_metric`: kalkulasi histogram frekuensi nilai numerik.
   - `correlation_matrix`: matriks korelasi Pearson antar kolom numerik.
2. Setiap implementasi tool menyusun query DuckDB terparameter menggunakan query compiler internal.
3. Periksa cache Redis `rc:{tool_name}:{args_hash}`. Jika hit, gunakan hasil cache; jika miss, jalankan DuckDB dan simpan ke Redis (TTL 24 jam).
4. Bungkus hasil dalam model Pydantic `ResultEnvelope`:
   - `columns`, `rows` (maksimal 1.000 baris tersimpan), `row_count`, `execution_time_ms`, `summary_stats`.
5. Validasi invarian: pastikan tidak ada nilai `NaN` atau `Infinity` yang tidak tertangani dalam payload JSON.

**Expected Files:**
- `apps/engine/app/tools/registry.py`
- `apps/engine/app/tools/implementations.py`
- `apps/engine/app/schemas/result.py`

**Technical Requirements:**
- DuckDB hanya memiliki akses read-only ke file Parquet versi aktif.
- Dilarang membuat query SQL bebas dari teks prompt LLM.

**Testing Requirements:**
- Unit test eksekusi setiap tool pada dataset benchmark Parquet dan verifikasi keakuratan kalkulasi matematika.

**Acceptance Criteria:**
- Seluruh 5 tool analitik mengeksekusi kalkulasi deterministik dengan benar dan mengembalikan `ResultEnvelope` tervalidasi.

---

### Task ID: TASK-605
**Task Name:** Grounded Narrator, Anti-Hallucination Checker & Chart Spec Engine
| Field | Description |
|---|---|
| Priority | P0 |
| Complexity | Large |
| Status | Planned |
| Dependencies | TASK-604 |
| Related Requirements | F-18, F-19 |

**Objective:**
Membangun modul narasi hasil analisis yang diverifikasi oleh Grounding Checker anti-halusinasi serta generator ChartSpec deterministik.

**Implementation Steps:**
1. Di `apps/engine/app/orchestration`, buat `narrator.py`.
2. Kirim hanya subset ringkasan agregat (<50 baris) dari `ResultEnvelope` dan pertanyaan asli ke LLM Narrator dengan prompt sistem pelaporan bisnis terstruktur.
3. Buat modul `grounding_checker.py`:
   - Ekstrak seluruh token angka numerik, persentase, dan mata uang dari teks narasi yang dihasilkan LLM menggunakan regex.
   - Bandingkan setiap angka terhadap himpunan nilai numerik yang ada di dalam `ResultEnvelope` (dengan toleransi pembulatan desimal yang wajar).
   - Jika ditemukan angka yang tidak ada dalam hasil data (halusinasi angka), picu regenerasi narasi satu kali. Jika tetap gagal, gunakan narasi berbasis template deterministik standar.
4. Buat modul `visualization_rules.py`:
   - Menganalisis bentuk data `ResultEnvelope`:
     - 1 kategori + 1 metrik (<= 8 item) -> `pie` atau `bar`.
     - 1 kategori + 1 metrik (> 8 item) -> horizontal `bar`.
     - Tanggal/Waktu + 1 metrik -> `line`.
     - 2 metrik numerik -> `scatter` (maks 2.000 titik).
   - Menghasilkan payload JSON `ChartSpec` yang valid.

**Expected Files:**
- `apps/engine/app/orchestration/narrator.py`
- `apps/engine/app/orchestration/grounding_checker.py`
- `apps/engine/app/orchestration/visualization_rules.py`
- `apps/engine/app/schemas/chart.py`

**Technical Requirements:**
- Dilarang menampilkan angka narasi ke pengguna jika tidak lolos verifikasi grounding.
- Narasi dirender sebagai teks biasa/markdown aman, dilarang menghasilkan kode HTML eksternal.

**Testing Requirements:**
- Unit test grounding checker dengan simulasi narasi yang memuat angka halusinasi palsu (verifikasi penggantian ke template aman).

**Acceptance Criteria:**
- Narasi analisis terbukti 100% didasarkan pada data faktual dan `ChartSpec` terbentuk sesuai kaidah visualisasi.

---

## 4. API & Database Changes

### API Changes
| Method | Endpoint | Purpose | Authentication |
|---|---|---|---|
| POST | `/api/v1/analysis/sessions` | Membuat sesi analisis baru | Bearer JWT + Member |
| GET | `/api/v1/analysis/sessions` | Daftar sesi dalam workspace | Bearer JWT + Member |
| GET | `/api/v1/analysis/sessions/:id` | Detail sesi dan pesan percakapan | Bearer JWT + Member |
| POST | `/api/v1/analysis/sessions/:id/messages` | Kirim pertanyaan analisis (SSE stream) | Bearer JWT + Member |
| POST | `/v1/ai/analyze` (Engine Internal) | Eksekusi orkestrasi AI streaming | `X-Internal-Token` |

### Database Changes
Data disimpan pada tabel PostgreSQL:
- `analysis_sessions`: pencatatan judul dan waktu sesi.
- `analysis_messages`: pesan pengguna (`user`) dan jawaban asisten (`assistant`).
- `analysis_runs`: status run, token LLM yang dikonsumsi, error code jika ada.
- `analysis_results`: penyimpanan `result_json` (`ResultEnvelope`) dan `chart_spec_json` (`ChartSpec`).

---

## 5. Design & UI Implementation

- **Related Pages:** `/app/:workspaceId/analysis/:sessionId`.
- **Component Requirements:**
  - `ChatWindow`: area riwayat pesan percakapan dengan autoscroll.
  - `StageIndicator`: indikator progres langkah analisis ("Menyusun rencana...", "Menghitung data via DuckDB...", "Menyusun visualisasi...", "Selesai").
  - `PlanPanel`: panel accordion yang dapat dibuka untuk melihat transparansi langkah perhitungan (`AnalysisPlan`).
  - `MessageItem`: gelembung pesan pengguna dan jawaban asisten yang menyertakan kartu hasil dan tombol "Simpan ke Dashboard".
  - `EvidenceViewer`: popover yang menampilkan tabel data agregat yang menjadi dasar kesimpulan narasi.
- **Responsive Behavior:** Pada layar mobile, input prompt tetap sticky di bagian bawah layar dengan keyboard handling yang rapi.
- **Accessibility Requirements:** Output streaming asisten diperbarui dengan atribut `aria-live="polite"` agar screen reader dapat mengumumkan selesainya jawaban.

---

## 6. Security Requirements

- Strict Privacy Boundary: File mentah pengguna atau baris data lengkap dilarang dikirim ke API penyedia LLM. Hanya skema kolom, statistik ringkasan, dan agregat kecil (<50 baris) yang diizinkan.
- Eksekusi Deterministik Tanpa Kode Bebas: LLM tidak memiliki akses ke runtime interpreter Python, shell eksekusi, atau pembentukan SQL bebas.
- Kontrol Kuota & Rate: Setiap pemanggilan analisis diverifikasi terhadap sisa kuota harian run workspace sebelum LLM dipanggil.
- Header Korelasi: Header `X-Request-ID` diteruskan pada seluruh rantai panggilan untuk memudahkan investigasi audit jika terjadi kegagalan.

---

## 7. Testing & Verification

- **Unit Tests:**
  - Validasi skema model Pydantic `AnalysisPlan` dan `ResultEnvelope`.
  - Aturan pencocokan regex dan ekstraksi angka pada `GroundingChecker`.
  - Logika penentuan tipe chart pada `visualization_rules.py`.
- **Integration Tests:**
  - Skenario end-to-end analisis dengan Fake LLM client: input pertanyaan -> plan terbentuk -> tool DuckDB tereksekusi -> narasi grounded -> SSE event terkirim lengkap.
- **Verification Commands:**
  - `cd apps/engine && pytest tests/test_planner.py tests/test_tools.py tests/test_grounding.py`
  - `cd apps/api && go test -v ./internal/modules/analysis/...`

---

## 8. Commit Plan

### 8.1 Commit Strategy
Memisahkan orkestrasi sesi Go API -> streaming gateway SSE -> Engine Planner & Validator -> Tool Registry DuckDB -> Narrator & Grounding Checker -> antarmuka frontend chat.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-601 | feat | analysis | feat(analysis): implement session management and schema context builder in Go |
| 02 | TASK-602 | feat | analysis | feat(analysis): add SSE streaming gateway with Redis concurrency lock |
| 03 | TASK-603 | feat | engine | feat(engine): implement structured LLM planner and Pydantic semantic validator |
| 04 | TASK-604 | feat | engine | feat(engine): build deterministic DuckDB tool registry and ResultEnvelope cache |
| 05 | TASK-605 | feat | engine | feat(engine): implement grounded narrator, anti-hallucination check, and ChartSpec |
| 06 | TASK-601..605 | feat | web | feat(web): implement AI conversational analysis chat interface with StageIndicator |

### 8.3 Commit Details
- **Commit 01:** Menyertakan handler sesi, repositori analisis, dan modul context builder di Go.
- **Commit 02:** Menyertakan handler SSE streaming dan relay event di Go API.
- **Commit 03:** Menyertakan client LLM provider, prompt planner v1, dan skema Pydantic plan di Engine.
- **Commit 04:** Menyertakan implementasi 5 tool DuckDB dan modul validasi hasil di Engine.
- **Commit 05:** Menyertakan modul narrator, grounding checker anti-halusinasi, dan generator spesifikasi chart.
- **Commit 06:** Menyertakan komponen React UI chat, stage indicator, panel bukti, dan streaming hook.

### 8.4 Commit Boundaries
- Pipeline komputasi deterministik di Engine harus selesai dan lolos pengujian matematika sebelum dihubungkan ke streaming SSE frontend.

### 8.5 Commit Dependencies
- Commit 02 bergantung pada 01. Commit 04 bergantung pada 03. Commit 05 bergantung pada 04. Commit 06 bergantung pada integrasi API lengkap.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Outage atau latensi tinggi dari provider LLM eksternal | Analisis pengguna terhenti | Sedang | Terapkan timeout ketat 30 detik, 2 kali retry transient, dan pesan error informatif jika provider tidak dapat dijangkau. |
| Grounding Checker menolak narasi valid karena perbedaan format angka | Kegagalan narasi palsu | Sedang | Normalisasi format angka (contoh: koma desimal vs titik, penulisan "1.5 juta" vs "1500000") sebelum pencocokan. |
| Output planner tidak deterministik walau `temperature = 0` | Run gagal di tengah jalan, biaya panggilan naik | Tinggi (terukur) | Validator ketat + satu repair retry (§9.1 aturan 4); catat jumlah retry ke `analysis_runs`. |
| Model mengirim ekspresi SQL di field identifier | Injeksi identifier ke query DuckDB | Sedang (terukur) | Identifier harus match persis nama kolom schema + regex `^[A-Za-z0-9_.-]+$` (§9.1 aturan 4). |
| Plan bentuk-valid tapi makna salah (mis. "profit" dipetakan ke `amount`) | Angka benar secara komputasi, salah secara bisnis | Tinggi | `PlanPanel` wajib menampilkan plan ke pengguna; narasi tidak boleh jadi satu-satunya sumber. |
| WAF provider memblokir prompt berisi keyword SQL destruktif | Pertanyaan pengguna ditolak sebelum sampai ke model | Rendah | Engine wajib menangani body non-JSON (`text/html`) dan memetakan 403 ke `LLM_REQUEST_BLOCKED`, bukan 500 (§9.1 aturan 5). |
| Kuota provider tidak terbaca lewat API | Kehabisan kuota muncul sebagai error samar | Sedang | Jangan bergantung pada endpoint kuota provider; pakai kuota harian internal di Redis (Scope §1.2) sebagai sumber kebenaran. |
| Latensi p95 tinggi pada panggilan pembuka (cold start) | UI terasa hang | Sedang | Semua tahapan wajib di-stream lewat SSE `stage_start`; jangan menunggu narasi selesai baru render. |

### 9.1 Hasil Uji Provider LLM (pra-implementasi)

Uji dijalankan sebelum TASK-603 ditulis, memakai skema `AnalysisPlan` minimal
(`tool`, `column`, `aggregation`, `group_by?`, `bucket?`) dan lima kasus: pertanyaan lugas
(Indonesia), pertanyaan ambigu, schema lebar 22 kolom (time series), kolom yang tidak ada
(`profit`), dan percobaan injeksi SQL. Skrip probe bersifat ad-hoc di luar repo
(`llm-plan-probe.mjs`, `llm-schema-probe.mjs`); hasilnya dicatat di sini, bukan di-commit.

| Provider / model | `json_schema` strict | `json_object` | Catatan |
|---|---|---|---|
| OpenRouter `perplexity/pplx-decider-v1.3:free` | 0/3 — HTTP 400 | 0/3 | Model "decisions", menolak endpoint `chat/completions`. Tidak dapat dipakai. |
| OpenRouter `nvidia/nemotron-3.5-super-vl:free` | 4/5 | 0/5 | Membocorkan SQL ke identifier (`group_by: "date_trunc('month', created)"`), `sum` pada kolom teks. |
| Sumopod `deepseek-v4.1-flash-1017` | 4/5 | 0/5 | Plan bersih, tetapi `time_series_metric` menghilangkan `group_by` bila skema tidak kondisional. |
| Sumopod `claude-haiku-5-5` | 4/5 | 0/5 | Paling bersih dan tercepat (median ±2.2 s, p95 ±7.0 s). Satu kegagalan: `time_series_metric.group_by` = kolom teks. |

Kegagalan yang bukan disebabkan model (berlaku semua provider):
- `json_object` tidak pernah mematuhi enum: `tool` keluar sebagai `aggregate`, `query`, atau `sql`.
- Prompt berisi keyword SQL destruktif (`DROP TABLE`) dijawab **HTTP 403 Cloudflare** dengan body HTML, bukan JSON.

Aturan yang mengikat TASK-603 karena hasil uji ini:
1. Hanya `response_format: json_schema` + `strict: true`; tidak ada fallback `json_object`.
2. Skema wajib memakai aturan kondisional: `time_series_metric` → `required: [group_by, bucket]`. Field `time_column` tidak dipakai karena memicu kolom karangan.
3. Field `bucket` wajib berupa enum (`day`, `week`, `month`, `quarter`, `year`); model tidak boleh menyusun nama kolom turunan seperti `created_monthly`.
4. Validator menolak plan yang gagal kecocokan nama/tipe, lalu retry perbaikan **tepat satu kali**. Rata-rata ±17% run butuh retry pada model terbaik.
5. Respons non-2xx berbody HTML diperlakukan sebagai kegagalan provider (`LLM_REQUEST_BLOCKED` / `LLM_UNAVAILABLE`), tidak boleh diparse sebagai JSON dan tidak boleh menjadi 500 dengan stack trace.
6. Default pengembangan: `claude-haiku-5-5`; pembanding: `deepseek-v4.1-flash-1017`. Sebelum rilis, satu model berbayar stabil harus lolos probe yang sama.

---

## 10. Definition of Done

- [ ] Sesi analisis dapat dibuat dan riwayat pesan tersimpan di database.
- [ ] LLM Planner menghasilkan rencana terstruktur yang tervalidasi secara semantik.
- [ ] `PlanValidator` menolak plan dengan kolom/tool di luar allowlist, agregasi yang tidak sesuai tipe kolom, dan `time_series_metric` tanpa `group_by` bertipe date + `bucket`; retry perbaikan maksimal satu kali.
- [ ] Tool DuckDB mengeksekusi kalkulasi deterministik terhadap file Parquet tanpa kesalahan matematika.
- [ ] Grounding Checker berhasil mendeteksi dan mencegah narasi angka halusinasi.
- [ ] Aliran event SSE terkirim secara stabil dan mulus ke antarmuka web klien.
- [ ] Kegagalan provider LLM (timeout, 4xx berbody HTML/WAF, kuota habis) menghasilkan error terkontrol tanpa stack trace ke klien.
- [ ] Seluruh unit dan integration test AI loop analitik lulus 100%.

---

## 11. As-Built Notes

> Status tetap `Planned` mengikuti konvensi repo (plan 01–11 tidak di-flip ke Done);
> kemajuan dilacak di commit history dan `00-roadmap.md`. Bagian ini mencatat
> realisasi implementasi + bukti verifikasi agar isi plan cocok dengan kode.

### 11.1 Deviations dari spesifikasi
| Spec | As-built | Alasan |
|---|---|---|
| `orchestration/plan_validator.py` memuat konstanta + prompt + `plan_question` | `plan_validator.py` validator murni; planner dipisah ke `orchestration/planner.py` | Satu modul = satu tanggung jawab; validator bisa diuji tanpa LLM. |
| Schema plan/result/chart dalam satu modul | `schemas/plan.py`, `schemas/result.py`, `schemas/chart.py` | Batas kontrak jelas dan import tidak melingkar. |
| `POST /v1/ai/analyze` mengembalikan plan + hasil | Sama, ditambah field `usage` (`prompt_tokens`, `completion_tokens`, `calls`) di frame `run_completed` | Go menyimpan biaya token per run di `analysis_runs`. |
| Kuota harian "diverifikasi" (tanpa mekanisme) | Counter Redis `quota:analysis:{workspace}:{yyyymmdd}` (TTL sampai akhir hari UTC), cek sebelum run dibuat | Satu-satunya batas biaya yang tidak bergantung API provider. Redis gagal → fail-open. |
| Route UI hanya `/app/:workspaceId/analysis/:sessionId` | Ditambah `/app/:workspaceId/analysis` (daftar sesi) + `GET /workspaces/:id/analysis/sessions` | Tanpa daftar, sesi lama tidak bisa dibuka kembali. |
| Risk §9 "catat jumlah retry ke `analysis_runs`" | `repair_attempts` dikirim di frame `plan_generated` dan ditampilkan di `PlanPanel`, tetapi **tidak** ada kolom khusus di `analysis_runs` | Menghindari migrasi kolom; nilai tetap tersedia ke klien. |
| Chart dirender library | SVG inline deterministik (`bar`/`line`/`scatter`/`pie` + tabel bukti) | Tidak menambah dependency; bentuk chart sudah dipilih engine. |
| Tombol "Simpan ke Dashboard" | Dirender nonaktif (`Tersedia pada plan dashboard`) | Persistensi dashboard di luar scope Plan 06. |
| `PlanValidator` juga menolak `sum`/`avg` pada kolom non-numerik | Ditambahkan (`TOOL_TYPE_MISMATCH` di validator + tool) | Temuan probe: model memilih agregasi numerik pada kolom teks. |
| Identifier harus match schema + regex | Tool mencocokkan nama ke skema Parquet dan mengutipnya; nilai selalu parameter terikat | Tidak ada teks model yang menjadi SQL. |

### 11.2 Bukti verifikasi (lokal, Windows)
- Engine: `pytest` → **93 passed, 2 skipped**. Mencakup skema plan, validator semantik, `planner.py` (JSON schema + tepat satu repair), 5 tool DuckDB, grounding checker, `choose_chart`, dan pipeline SSE dengan fake LLM.
- Go: `go build ./...` = 0, `go vet ./...` = 0, `go test -count=1 ./...` semua paket `ok`. Mencakup 6 test context builder, 5 test service DB nyata, 7 test SSE/lock/relay, quota (2 test Redis + 1 test handler 429), dan parser stream engine (3 test).
- Web: `npm test` → **19 passed** (SSE parser lintas chunk, mapping event, streaming end-to-end dengan `ReadableStream` mock, error 429); `npm run build` = 0.
- E2E live engine (provider nyata `claude-haiku-5-5`, Parquet 20 baris): urutan frame `stage_start → plan_generated → stage_start → tool_executed → stage_start → narrative_chunk×N → chart_ready → run_completed`; `grounded=true`, `chart=bar`, `usage={prompt_tokens:2855, completion_tokens:1091, calls:3}`.
- E2E live stack penuh (API 8080 + engine 8000 + PostgreSQL/Redis/Mailpit nyata), register → OTP dari Mailpit → verify → membership → buat sesi → tanya:
  - Frame: `stage_start → plan_generated → stage_start → tool_executed → stage_start → narrative_chunk×4 → chart_ready → run_completed`.
  - Persistensi: `analysis_messages` = `user` + `assistant`; `analysis_runs.status=completed`, `prompt_tokens=1999`, `completion_tokens=401`; `analysis_results` = 1 baris.
  - `GET /api/v1/config/limits` memuat `analysis.daily_runs_per_workspace`.

### 11.3 Belum tercakup
- Lock konkurensi & kuota diuji pada level handler, bukan dua stream HTTP paralel di E2E.
- Sifat incremental flush dibuktikan lewat `curl -N` dan test relay; belum ada tes timing browser.
- a11y `aria-live` belum diuji dengan screen reader otomatis.
- Tombol "Simpan ke Dashboard" dan persistensi dashboard menunggu plan dashboard.
- Stress/kuota provider berbayar dan pemilihan model rilis dilakukan sebelum produksi (§9.1 aturan 6).
