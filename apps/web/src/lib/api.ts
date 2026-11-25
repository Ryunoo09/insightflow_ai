import { useAuthStore } from "../features/auth/store";

/** Base path for the Go API. Vite proxies /api in development. */
export const API_BASE = import.meta.env.VITE_API_BASE_URL ?? "/api/v1";

/** ApiError mirrors the backend error envelope: {code, message, details, request_id}. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details?: unknown;
  readonly requestId?: string;

  constructor(status: number, code: string, message: string, details?: unknown, requestId?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
    this.requestId = requestId;
  }
}

type RequestOptions = {
  method?: "GET" | "POST" | "PATCH" | "DELETE";
  body?: unknown;
  /** Send the bearer token (default true). */
  auth?: boolean;
  /** Retry once after a successful token refresh (default true). */
  retry?: boolean;
};

async function send(path: string, options: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = { "X-Requested-With": "InsightFlow" };
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  const token = useAuthStore.getState().accessToken;
  if (token && options.auth !== false) headers.Authorization = `Bearer ${token}`;

  return fetch(`${API_BASE}${path}`, {
    method: options.method ?? "GET",
    headers,
    credentials: "include",
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
}

let refreshInFlight: Promise<boolean> | null = null;

/** Rotates the refresh cookie once, even when several 401s race. */
export function refreshSession(): Promise<boolean> {
  refreshInFlight ??= (async () => {
    try {
      const res = await send("/auth/refresh", { method: "POST", auth: false });
      if (!res.ok) {
        useAuthStore.getState().clear();
        return false;
      }
      const data = (await res.json()) as { access_token: string };
      useAuthStore.getState().setAccessToken(data.access_token);
      return true;
    } catch {
      useAuthStore.getState().clear();
      return false;
    } finally {
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  let res = await send(path, options);
  if (res.status === 401 && options.auth !== false && options.retry !== false) {
    if (await refreshSession()) res = await send(path, options);
  }
  if (res.status === 204) return undefined as T;

  const isJson = res.headers.get("content-type")?.includes("application/json") ?? false;
  const data = isJson ? await res.json() : undefined;
  if (!res.ok) {
    const error = (data as { error?: { code?: string; message?: string; details?: unknown; request_id?: string } } | undefined)?.error;
    throw new ApiError(
      res.status,
      error?.code ?? "UNKNOWN",
      error?.message ?? "Terjadi kesalahan. Coba lagi.",
      error?.details,
      error?.request_id,
    );
  }
  return data as T;
}