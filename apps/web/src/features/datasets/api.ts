import { API_BASE, ApiError, api, refreshSession } from "../../lib/api";
import { useAuthStore } from "../auth/store";

export type DatasetVersion = {
  version_no: number;
  status: string;
  row_count?: number;
  column_count?: number;
  source_size_bytes?: number;
  error?: { code: string; message: string };
};

export type Dataset = {
  id: string;
  workspace_id: string;
  name: string;
  original_filename: string;
  file_format: string;
  status: string;
  current_version: DatasetVersion;
  quality_score?: number;
  created_at: string;
  updated_at: string;
};

export type DatasetStatus = {
  id: string;
  status: string;
  version_no: number;
  error?: { code: string; message: string };
};

export type DatasetListParams = {
  page?: number;
  perPage?: number;
  q?: string;
  status?: string;
  sort?: string;
  order?: "asc" | "desc";
};

export type DatasetListMeta = { page: number; per_page: number; total: number };

export type DatasetList = { data: Dataset[]; meta: DatasetListMeta };

/** One dataset version summary for the explorer version switcher. */
export type DatasetVersionInfo = {
  version_no: number;
  status: string;
  row_count?: number;
  column_count?: number;
  created_at: string;
};

export type ExplorerSort = { column: string; direction: "asc" | "desc" };

export type ExplorerQuery = {
  offset: number;
  limit: number;
  versionNo?: number;
  sort?: ExplorerSort[];
  search?: string;
};

export type ExplorerColumn = { name: string; type: string; original_name?: string };

export type ExplorerRows = {
  columns: ExplorerColumn[];
  rows: Record<string, unknown>[];
  total: number;
};

/** Builds the explorer request body (URL/param shape stays server-owned). */
export function buildRowsQuery(query: ExplorerQuery): Record<string, unknown> {
  const body: Record<string, unknown> = { offset: query.offset, limit: query.limit };
  if (query.versionNo) body.version_no = query.versionNo;
  if (query.sort?.length) body.sort = query.sort;
  if (query.search?.trim()) body.search = query.search.trim();
  return body;
}

export function queryRows(datasetId: string, query: ExplorerQuery): Promise<ExplorerRows> {
  return api<ExplorerRows>(`/datasets/${datasetId}/rows/query`, { method: "POST", body: buildRowsQuery(query) });
}

export function listVersions(datasetId: string): Promise<{ data: DatasetVersionInfo[] }> {
  return api<{ data: DatasetVersionInfo[] }>(`/datasets/${datasetId}/versions`);
}

/** Public limits from GET /config/limits (no secrets). */
export type Limits = {
  upload: { csv_max_bytes: number; xlsx_max_bytes: number };
  dataset: {
    max_per_workspace: number;
    max_rows_csv: number;
    max_rows_xlsx: number;
    max_columns: number;
  };
  profile: { timeout_seconds: number };
  analysis: { daily_runs_per_workspace: number };
  auth: { google_enabled: boolean };
};

const SORTABLE = ["name", "created_at", "updated_at", "row_count", "byte_size", "status"] as const;

/** Builds the query string for the server-side dataset list (URL-shareable). */
export function buildDatasetListQuery(params: DatasetListParams): string {
  const search = new URLSearchParams();
  if (params.page) search.set("page", String(params.page));
  if (params.perPage) search.set("per_page", String(params.perPage));
  if (params.q?.trim()) search.set("q", params.q.trim());
  if (params.status) search.set("status", params.status);
  if (params.sort && (SORTABLE as readonly string[]).includes(params.sort)) search.set("sort", params.sort);
  if (params.order) search.set("order", params.order);
  return search.toString();
}

export function listDatasets(workspaceId: string, params: DatasetListParams = {}): Promise<DatasetList> {
  const qs = buildDatasetListQuery(params);
  return api<DatasetList>(`/workspaces/${workspaceId}/datasets${qs ? `?${qs}` : ""}`);
}

export function renameDataset(datasetId: string, name: string): Promise<Dataset> {
  return api<Dataset>(`/datasets/${datasetId}`, { method: "PATCH", body: { name } });
}

export function deleteDataset(datasetId: string): Promise<void> {
  return api<void>(`/datasets/${datasetId}`, { method: "DELETE" });
}

export function reprocessDataset(datasetId: string): Promise<Dataset> {
  return api<Dataset>(`/datasets/${datasetId}/reprocess`, { method: "POST" });
}

export function fetchLimits(): Promise<Limits> {
  return api<Limits>("/config/limits", { auth: false });
}

export const datasetsApi = {
  list: listDatasets,
  get: (datasetId: string) => api<Dataset>(`/datasets/${datasetId}`),
  status: (datasetId: string) => api<DatasetStatus>(`/datasets/${datasetId}/status`),
  upload: uploadDataset,
  rename: renameDataset,
  remove: deleteDataset,
  reprocess: reprocessDataset,
  limits: fetchLimits,
  versions: listVersions,
  rows: queryRows,
};

/** Streams a file with XHR so upload progress can be reported. */
function uploadDataset(
  workspaceId: string,
  file: File,
  name: string,
  onProgress: (percent: number) => void,
): Promise<Dataset> {
  return new Promise((resolve, reject) => {
    const send = (token: string | null, retried: boolean) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", `${API_BASE}/workspaces/${workspaceId}/datasets`);
      xhr.withCredentials = true;
      xhr.setRequestHeader("X-Requested-With", "InsightFlow");
      if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`);
      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable) onProgress(Math.round((event.loaded / event.total) * 100));
      };
      xhr.onload = async () => {
        if (xhr.status === 202) {
          resolve(JSON.parse(xhr.responseText) as Dataset);
          return;
        }
        if (xhr.status === 401 && !retried && (await refreshSession())) {
          send(useAuthStore.getState().accessToken, true);
          return;
        }
        reject(toApiError(xhr));
      };
      xhr.onerror = () => reject(new ApiError(0, "NETWORK", "Koneksi ke server gagal."));

      const form = new FormData();
      if (name.trim()) form.append("name", name.trim());
      form.append("file", file, file.name);
      xhr.send(form);
    };
    send(useAuthStore.getState().accessToken, false);
  });
}

function toApiError(xhr: XMLHttpRequest): ApiError {
  let parsed: { error?: { code?: string; message?: string; details?: unknown; request_id?: string } } | undefined;
  try {
    parsed = JSON.parse(xhr.responseText);
  } catch {
    parsed = undefined;
  }
  return new ApiError(
    xhr.status,
    parsed?.error?.code ?? "UNKNOWN",
    parsed?.error?.message ?? "Unggah gagal.",
    parsed?.error?.details,
    parsed?.error?.request_id,
  );
}

/** Client-side convenience limits (server enforces the real ones). */
export const UPLOAD_LIMITS = { csvBytes: 50 * 1024 * 1024, xlsxBytes: 20 * 1024 * 1024 };
