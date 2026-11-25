# Plan 07: Dashboards, Visualizations & Saved Findings

## 1. Overview

| Field | Value |
|---|---|
| Plan ID | PLAN-007 |
| Name | Dashboards, Recharts Visualizations & Saved Findings |
| Priority | P1 – High |
| Complexity | Medium |
| Status | Planned |
| Depends On | PLAN-006 |
| Related Requirements | F-20, F-21 (Chart Rendering & Saved Findings), F-22, F-23 (Dashboard Grid & Widget Re-run) |

### 1.1 Objective
Membangun antarmuka dashboard interaktif dan mesin rendering visualisasi data berbasis Recharts yang mampu menampilkan chart dari `ChartSpec` tervalidasi, mengelola grid dashboard 12 kolom yang dapat diatur ulang, menyimpan temuan analisis langsung dari sesi chat, serta mengkalkulasi ulang query widget terhadap versi dataset aktif.

### 1.2 Scope
**In Scope:**
- Frontend Web: Komponen `ChartRenderer` serbaguna berbasis Recharts untuk tipe chart MVP (`bar`, `line`, `scatter`, `pie`) dengan palet warna semantic data visualization.
- Penyimpanan Temuan Analisis: aksi satu-klik dari pesan chat AI untuk menyimpan hasil visualisasi dan ringkasan teks ke koleksi temuan atau dashboard tertentu.
- Dashboard Builder: Grid dashboard 12 kolom responsif (`react-grid-layout` atau CSS Grid modular), kartu widget dengan aksi ubah ukuran (small, medium, full-width), ubah judul, hapus, dan buka detail sumber.
- Rekalkulasi Widget (*Widget Re-run*): tombol penyegaran data widget yang mengirimkan kembali `query_spec` ke Go API -> Engine DuckDB untuk menghitung ulang angka terhadap data versi dataset aktif saat ini.
- Backend Go API: CRUD dashboard (`dashboards`), CRUD widget (`dashboard_widgets`), endpoint rekalkulasi widget `POST /api/v1/dashboards/:id/widgets/:widget_id/rerun`.

**Out of Scope:**
- Ekspor dashboard ke format PDF atau gambar resolusi tinggi (fitur P2 per PRD F-28).
- Tipe chart di luar MVP (seperti Sankey, Treemap, Choropleth map, Candlestick).
- Pengaturan filter global interaktif lintas seluruh widget dashboard.

### 1.3 Requirements Traceability
| Requirement ID | Deskripsi Kebutuhan | Rencana Implementasi | Sumber Dokumen |
|---|---|---|---|
| F-20 | Render visualisasi data Recharts dari ChartSpec | Komponen ChartRenderer & palet semantic | PRD Bagian 28 |
| F-21 | Simpan hasil analisis & temuan ke dashboard | Endpoint save result & modal dialog web | PRD Bagian 29 |
| F-22 | Dashboard grid 12 kolom responsif & susun widget | Layout dashboard web & persistence JSON | PRD Bagian 30 |
| F-23 | Rekalkulasi widget terhadap versi dataset aktif | Endpoint rerun widget & compiler DuckDB | PRD Bagian 31 |
| UX-CHART | Token warna chart semantic & format angka | Styling Recharts & Design.md | Design.md Bagian 4.2 |

---

## 2. Technical Context

Setiap visualisasi yang dihasilkan oleh AI Engine mematuhi skema JSON `ChartSpec`:
```json
{
  "chart_type": "bar",
  "title": "Pendapatan per Kategori Produk",
  "x_axis": { "field": "category", "label": "Kategori" },
  "y_axis": { "field": "total_revenue", "label": "Pendapatan (IDR)", "format": "currency" },
  "series": [{ "field": "total_revenue", "name": "Total Pendapatan" }],
  "data": [
    { "category": "Elektronik", "total_revenue": 15000000 },
    { "category": "Pakaian", "total_revenue": 8500000 }
  ]
}
```
Ketika pengguna menekan "Simpan ke Dashboard", Go API menyimpan metadata widget bersama salinan `query_spec` asli (argumen tool analitik dan query yang mendasarinya). Ketika versi dataset diperbarui (misal versi 2 diunggah), widget dapat dihitung ulang dengan menjalankan kembali `query_spec` terhadap file Parquet versi 2 melalui endpoint rerun.

---

## 3. Implementation Tasks

### Task ID: TASK-701
**Task Name:** Recharts Visualizer & Chart Renderer Component
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | PLAN-006 |
| Related Requirements | F-20, UX-CHART |

**Objective:**
Membangun komponen presentasi chart React serbaguna yang membaca payload `ChartSpec` dan merender chart Recharts yang adaptif terhadap tema terang/gelap.

**Implementation Steps:**
1. Di `apps/web/src/components/charts`, buat `ChartRenderer.tsx`.
2. Implementasikan rendering kondisional berdasarkan `chart_type`:
   - `bar`: BarChart Recharts dengan orientasi vertikal atau horizontal, grid dashed lembut, tooltip format angka.
   - `line`: LineChart dengan kurva monotone, dot penanda data, dan multi-series pendukung.
   - `scatter`: ScatterChart untuk korelasi dua metrik numerik.
   - `pie`: PieChart donat dengan legend posisi bawah dan persentase label.
3. Petakan palet warna chart dari token visual `Design.md`:
   - Series 1: Primary Indigo (`#4338CA` / `#6366F1`)
   - Series 2: Cyan (`#06B6D4`)
   - Series 3: Emerald (`#10B981`)
   - Series 4: Amber (`#F59E0B`)
   - Series 5: Rose (`#F43F5E`)
4. Bungkus dalam `ResponsiveContainer` agar chart menyesuaikan lebar wadah kartu secara proporsional.
5. Tambahkan fallback jika data kosong atau format spec tidak valid.

**Expected Files:**
- `apps/web/src/components/charts/ChartRenderer.tsx`
- `apps/web/src/components/charts/chartUtils.ts`
- `apps/web/src/components/charts/CustomTooltip.tsx`

**Technical Requirements:**
- Semua format angka sumbu dan tooltip menggunakan aturan angka Indonesia/internasional tabular.
- Komponen wajib mendukung mode gelap tanpa merusak kontras teks sumbu.

**Testing Requirements:**
- Storybook atau test component rendering untuk setiap tipe chart dengan mock dataset.

**Acceptance Criteria:**
- Keempat tipe chart ter-render dengan rapi, responsif, dan menampilkan tooltip yang informatif saat disentuh pointer.

---

### Task ID: TASK-702
**Task Name:** Save Findings & Widget Persistence API
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-701 |
| Related Requirements | F-21, F-22 |

**Objective:**
Mengimplementasikan use-case penyimpanan temuan analisis dan pengelolaan widget dashboard di Go API.

**Implementation Steps:**
1. Di `apps/api/internal/modules/dashboards`, buat `handler.go`, `service.go`, dan `repository.go`.
2. Implementasikan endpoint:
   - `POST /api/v1/dashboards`: membuat dashboard baru (judul, deskripsi).
   - `GET /api/v1/dashboards`: daftar dashboard dalam workspace.
   - `GET /api/v1/dashboards/:id`: detail dashboard beserta seluruh widget di dalamnya.
   - `PUT /api/v1/dashboards/:id/layout`: menyimpan tata letak posisi dan ukuran widget (`layout_json`).
   - `POST /api/v1/dashboards/:id/widgets`: menambahkan widget baru (menyimpan `query_spec_json`, `chart_spec_json`, judul, tipe).
   - `DELETE /api/v1/dashboards/:id/widgets/:widget_id`: menghapus widget dari dashboard.
3. Tambahkan verifikasi otorisasi keanggotaan workspace pada setiap operasi CRUD.

**Expected Files:**
- `apps/api/internal/modules/dashboards/handler.go`
- `apps/api/internal/modules/dashboards/service.go`
- `apps/api/internal/modules/dashboards/repository.go`

**Technical Requirements:**
- Payload layout disimpan dalam format JSONB terstruktur (array objek `{ widget_id, x, y, w, h }`).

**Testing Requirements:**
- Integration test CRUD dashboard dan pembaruan layout JSONB.

**Acceptance Criteria:**
- Pengguna dapat membuat dashboard, menyimpan widget, dan memperbarui konfigurasi tata letak di PostgreSQL.

---

### Task ID: TASK-703
**Task Name:** Responsive 12-Column Dashboard Grid UI
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-702 |
| Related Requirements | F-22 |

**Objective:**
Membangun antarmuka pengguna dashboard dengan grid 12 kolom responsif untuk menyusun, mengubah ukuran, dan menampilkan widget visual.

**Implementation Steps:**
1. Di `apps/web/src/features/dashboards/components`, buat `DashboardGrid.tsx` dan `WidgetCard.tsx`.
2. Implementasikan layout grid:
   - 12 kolom pada layar desktop (>1024px).
   - 6 kolom pada layar tablet (768px–1024px).
   - 1 kolom penuh pada layar ponsel (<768px).
3. Buat komponen kartu `WidgetCard`:
   - Header widget: Judul widget, badge tipe analisis, tombol menu (Ubah ukuran, Kalkulasi ulang, Hapus).
   - Body widget: Merender `ChartRenderer` atau ringkasan metrik angka besar.
   - Footer widget: Waktu terakhir dikalkulasi dan tombol \"Buka Sumber Analisis\".
4. Implementasikan modal \"Simpan ke Dashboard\" di antarmuka chat percakapan agar pengguna dapat memilih dashboard tujuan langsung setelah AI menjawab.

**Expected Files:**
- `apps/web/src/features/dashboards/components/DashboardGrid.tsx`
- `apps/web/src/features/dashboards/components/WidgetCard.tsx`
- `apps/web/src/features/dashboards/components/SaveFindingModal.tsx`
- `apps/web/src/features/dashboards/pages/DashboardDetailPage.tsx`

**Technical Requirements:**
- State posisi grid tersimpan secara otomatis (debounced 1 detik) setelah widget dipindahkan atau diubah ukurannya.

**Testing Requirements:**
- Component test rendering grid dengan 4 widget berbeda ukuran.

**Acceptance Criteria:**
- Dashboard menampilkan kumpulan widget dengan visual yang proporsional dan kartu dapat diatur tata letaknya.

---

### Task ID: TASK-704
**Task Name:** Widget Re-calculation Engine & Active Version Refresh
| Field | Description |
|---|---|
| Priority | P1 |
| Complexity | Medium |
| Status | Planned |
| Dependencies | TASK-702, PLAN-006 |
| Related Requirements | F-23 |

**Objective:**
Membangun alur kerja penyegaran widget untuk mengeksekusi kembali kalkulasi deterministik terhadap versi dataset yang sedang aktif.

**Implementation Steps:**
1. Di Go API, implementasikan `POST /api/v1/dashboards/:id/widgets/:widget_id/rerun`:
   - Ambil `query_spec_json` dari database untuk widget terkait.
   - Ambil versi aktif terbaru (`current_version_no`) dari dataset yang terhubung.
   - Panggil Engine internal `POST /v1/ai/recalculate` membawa `query_spec` dan `dataset_ref` versi aktif.
2. Di Python Engine, buat endpoint internal `POST /v1/ai/recalculate`:
   - Jalankan tool DuckDB deterministik yang sesuai dengan parameter pada `query_spec` terhadap Parquet versi aktif.
   - Bentuk `ResultEnvelope` baru dan perbarui `ChartSpec` baru.
   - Kembalikan data segar ke Go API.
3. Go API memperbarui kolom `chart_spec_json` pada `dashboard_widgets`, memperbarui timestamp `updated_at`, dan mengembalikan widget termutakhir ke browser.
4. Di frontend, tambahkan tombol refresh pada kartu widget dengan indikator loading berputar saat rekalkulasi berlangsung.

**Expected Files:**
- `apps/api/internal/modules/dashboards/rerun_handler.go`
- `apps/engine/app/api/v1/recalculate.py`
- `apps/web/src/features/dashboards/hooks/useWidgetRerun.ts`

**Technical Requirements:**
- Jika versi dataset baru kehilangan kolom yang dibutuhkan oleh widget, sistem wajib menangani kegagalan dengan anggun dan mengembalikan status peringatan (`COLUMN_NOT_FOUND_IN_ACTIVE_VERSION`) tanpa merusak widget yang ada.

**Testing Requirements:**
- Integration test rerun: mengubah file Parquet versi baru, memicu rerun, dan memastikan nilai numerik chart terbarui.

**Acceptance Criteria:**
- Nilai data widget berhasil diperbarui terhadap versi aktif dataset hanya dengan menekan satu tombol.

---

## 4. API & Database Changes

### API Changes
| Method | Endpoint | Purpose | Authentication |
|---|---|---|---|
| POST | `/api/v1/dashboards` | Membuat dashboard baru | Bearer JWT + Member |
| GET | `/api/v1/dashboards` | Mengambil daftar dashboard workspace | Bearer JWT + Member |
| GET | `/api/v1/dashboards/:id` | Mengambil detail dashboard & daftar widget | Bearer JWT + Member |
| PUT | `/api/v1/dashboards/:id/layout` | Memperbarui tata letak posisi widget | Bearer JWT + Member |
| POST | `/api/v1/dashboards/:id/widgets` | Menyimpan temuan analisis sebagai widget | Bearer JWT + Member |
| DELETE | `/api/v1/dashboards/:id/widgets/:widget_id` | Menghapus widget dari dashboard | Bearer JWT + Member |
| POST | `/api/v1/dashboards/:id/widgets/:widget_id/rerun` | Menghitung ulang data widget terhadap versi aktif | Bearer JWT + Member |
| POST | `/v1/ai/recalculate` (Engine Internal) | Eksekusi tool DuckDB deterministik ulang | `X-Internal-Token` |

### Database Changes
Menggunakan tabel yang sudah disiapkan pada Plan 02:
- `dashboards`: `id`, `workspace_id`, `title`, `description`, `layout_json`, `created_at`, `updated_at`.
- `dashboard_widgets`: `id`, `dashboard_id`, `title`, `widget_type`, `query_spec_json`, `chart_spec_json`, `created_at`.

---

## 5. Design & UI Implementation

- **Related Pages:**
  - `/app/:workspaceId/dashboards`: Galeri kartu dashboard dengan tombol \"Buat Dashboard\".
  - `/app/:workspaceId/dashboards/:id`: Tampilan grid dashboard 12 kolom dengan daftar widget.
- **Component Requirements:**
  - `WidgetCard`: Wadah kartu putih (atau gelap) dengan border halus `--border`, shadow token `sm`, dan aksi kontrol di header.
  - `EmptyDashboardState`: Tampilan kosong yang ramah dengan ilustrasi dan tombol aksi cepat untuk mengarahkan pengguna ke sesi analisis.
  - `SaveFindingModal`: Dialog pemilihan dashboard yang dilengkapi dropdown opsi dashboard yang sudah ada atau form instan membuat dashboard baru.
- **Design Tokens:**
  - Semantic visualization palette: Indigo, Cyan, Emerald, Amber, Rose untuk data series.
  - Padding kartu standar 16px (`p-4`) hingga 24px (`p-6`).
- **Accessibility Requirements:**
  - Seluruh elemen grafik memiliki label tekstual alternatif (deskripsi grafik di balik atribut `aria-label`).
  - Navigasi keyboard pada kontrol kartu widget (tombol refresh dan menu dapat dijangkau tombol Tab).

---

## 6. Security Requirements

- Validasi Kepemilikan Workspace: Endpoint CRUD dashboard dan widget selalu memverifikasi bahwa `dashboard_id` berada di dalam workspace pengguna yang sedang login.
- Sanitasi Input Judul: Judul dan deskripsi dashboard dibersihkan dari tag HTML dan karakter berisiko XSS.
- Proteksi Rekalkulasi: Rekalkulasi widget hanya mengeksekusi parameter query yang sudah tersimpan di database; pengguna dilarang memanipulasi string query pada saat request refresh dikirim.

---

## 7. Testing & Verification

- **Unit Tests:**
  - Logika pemetaan data `ChartRenderer` ke props Recharts.
  - Validasi skema payload `ChartSpec` di frontend.
- **Integration Tests:**
  - Siklus penyimpanan temuan: hasil chat disimpan -> widget muncul pada dashboard -> layout disimpan ke DB.
  - Siklus rekalkulasi: verifikasi pemanggilan DuckDB internal dan pembaruan `chart_spec_json`.
- **Verification Commands:**
  - `cd apps/api && go test -v ./internal/modules/dashboards/...`
  - `cd apps/engine && pytest tests/test_recalculate.py`
  - `cd apps/web && npm test`

---

## 8. Commit Plan

### 8.1 Commit Strategy
Memisahkan komponen rendering Recharts -> modul Go API dashboard & widget -> layout grid UI frontend -> modul rekalkulasi widget.

### 8.2 Commit Breakdown
| Order | Related Tasks | Commit Type | Commit Scope | Commit Message |
|---|---|---|---|---|
| 01 | TASK-701 | feat | web | feat(web): implement Recharts visualizer component with semantic palette |
| 02 | TASK-702 | feat | dashboards | feat(dashboards): implement dashboard and widget CRUD persistence in Go API |
| 03 | TASK-703 | feat | web | feat(web): build responsive 12-column dashboard grid and SaveFinding modal |
| 04 | TASK-704 | feat | dashboards | feat(dashboards): add widget re-run engine against active dataset version |

### 8.3 Commit Details
- **Commit 01:** Menyertakan komponen `ChartRenderer.tsx`, tooltip kustom, dan utilitas format angka.
- **Commit 02:** Menyertakan handler, repositori, dan migrasi query untuk modul dashboards di Go API.
- **Commit 03:** Menyertakan komponen `DashboardGrid.tsx`, kartu widget, modal penyimpanan temuan, dan halaman detail dashboard.
- **Commit 04:** Menyertakan endpoint internal rekalkulasi di Engine dan handler re-run di Go API.

### 8.4 Commit Boundaries
- Komponen rendering chart harus teruji secara visual sebelum digabungkan ke kartu dashboard.

### 8.5 Commit Dependencies
- Commit 02 bergantung pada Plan 02. Commit 03 bergantung pada 01 dan 02. Commit 04 bergantung pada Plan 06 dan Commit 02.

---

## 9. Risks & Open Questions

| Risk / Question | Impact | Likelihood | Mitigation / Proposed Resolution |
|---|---|---|---|
| Recharts lambat saat merender scatter plot dengan ribuan titik data | Penurunan framerate browser | Sedang | Batasi titik scatter plot maksimal 2.000 titik dari sisi Engine DuckDB. |
| Dataset versi baru memiliki perubahan tipe kolom yang membatalkan widget | Widget error saat di-recalculate | Rendah | Kembalikan pesan status error deskriptif pada kartu widget tanpa menghapus data visualisasi sebelumnya. |

---

## 10. Definition of Done

- [ ] Komponen `ChartRenderer` merender keempat tipe chart MVP (bar, line, scatter, pie) dengan benar.
- [ ] Temuan analisis dari chat berhasil disimpan ke dalam dashboard.
- [ ] Grid dashboard 12 kolom merender widget secara responsif dan mempertahankan tata letak di database.
- [ ] Tombol re-run widget berhasil memperbarui data grafik terhadap versi aktif dataset.
- [ ] Seluruh unit dan integration test dashboard lulus 100%.
