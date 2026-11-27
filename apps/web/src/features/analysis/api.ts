import { API_BASE, ApiError, api, refreshSession } from "../../lib/api";
import { useAuthStore } from "../auth/store";

/** One analysis session (Plan 06). */
export type AnalysisSession = {
  id: string;
  workspace_id: string;
  dataset_id: string;
  title: string;
  created_at: string;
  updated_at: string;
};

export type AnalysisSessionList = {
  data: AnalysisSession[];
  meta: { page: number; per_page: number; total: number };
};

export type AnalysisMessage = {
  id: string;
  role: "user" | "assistant" | "system";
  content: string;
  created_at: string;
};

export type ContextColumn = {
  name: string;
  original_name?: string;
  type: string;
  nullable?: boolean;
};

export type AnalysisContext = {
  dataset: {
    dataset_id: string;
    version_no: number;
    row_count: number;
    column_count: number;
    quality_score?: number | null;
    columns: ContextColumn[];
  };
  messages: { role: string; content: string }[];
};

export type AnalysisDetail = AnalysisSession & {
  messages: AnalysisMessage[];
  context: AnalysisContext;
};

export type AnalysisPlan = {
  tool: string;
  column: string;
  aggregation: string;
  group_by?: string | null;
  bucket?: string | null;
  filters?: Record<string, string> | null;
  intent: string;
};

export type ResultEnvelope = {
  tool: string;
  columns: { name: string; type: string }[];
  rows: Record<string, unknown>[];
  row_count: number;
  execution_time_ms: number;
  summary_stats: Record<string, unknown>;
  cache_hit?: boolean;
};

export type ChartSpec = {
  kind: "bar" | "line" | "scatter" | "pie" | "table";
  x?: string | null;
  y?: string | null;
  series: string[];
  title: string;
  reason: string;
};

/** Every event the engine may relay over SSE. */
export type AnalysisEvent =
  | { type: "stage_start"; stage: string; message: string }
  | { type: "plan_generated"; plan: AnalysisPlan; repair_attempts: number }
  | { type: "tool_executed"; tool: string; row_count: number; execution_time_ms: number; cache_hit: boolean }
  | { type: "narrative_chunk"; text: string }
  | { type: "chart_ready"; chart: ChartSpec }
  | {
      type: "run_completed";
      result: ResultEnvelope;
      narrative: string;
      narrative_grounded: boolean;
      narrative_fallback: string;
      chart: ChartSpec;
      usage?: { prompt_tokens: number; completion_tokens: number; calls: number };
      duration_ms: number;
    }
  | { type: "run_failed"; code: string; message: string; stage?: string };

export function listSessions(workspaceId: string): Promise<AnalysisSessionList> {
  return api<AnalysisSessionList>(`/workspaces/${workspaceId}/analysis/sessions`);
}

export function createSession(workspaceId: string, datasetId: string, title: string): Promise<AnalysisSession> {
  return api<AnalysisSession>(`/workspaces/${workspaceId}/analysis/sessions`, {
    method: "POST",
    body: { dataset_id: datasetId, title },
  });
}

export function getSession(sessionId: string): Promise<AnalysisDetail> {
  return api<AnalysisDetail>(`/analysis/sessions/${sessionId}`);
}

/**
 * Streams one question and calls onEvent for every SSE frame. Resolves when the
 * stream ends. The body is read incrementally, so the stage indicator moves
 * while the engine is still working.
 */
export async function streamMessage(
  sessionId: string,
  content: string,
  onEvent: (event: AnalysisEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const send = (token: string | null) =>
    fetch(`${API_BASE}/analysis/sessions/${sessionId}/messages`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Requested-With": "InsightFlow",
        Accept: "text/event-stream",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      credentials: "include",
      body: JSON.stringify({ content }),
      signal,
    });

  let res = await send(useAuthStore.getState().accessToken);
  if (res.status === 401 && (await refreshSession())) {
    res = await send(useAuthStore.getState().accessToken);
  }
  if (!res.ok) {
    // Errors before the stream starts still arrive as the JSON envelope.
    const body = (await res.json().catch(() => undefined)) as
      | { error?: { code?: string; message?: string } }
      | undefined;
    throw new ApiError(
      res.status,
      body?.error?.code ?? "UNKNOWN",
      body?.error?.message ?? "Analisis gagal dimulai.",
    );
  }
  if (!res.body) return;

  const push = createSSEParser();
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    for (const message of push(decoder.decode(value, { stream: true }))) {
      const parsed = toAnalysisEvent(message);
      if (parsed) onEvent(parsed);
    }
  }
}

/** Parses one SSE `event:`/`data:` pair into a typed analysis event. */
export function toAnalysisEvent({ event, data }: SSEMessage): AnalysisEvent | null {
  let payload: Record<string, unknown>;
  try {
    payload = JSON.parse(data) as Record<string, unknown>;
  } catch {
    return null;
  }
  switch (event) {
    case "stage_start":
      return { type: "stage_start", stage: String(payload.stage ?? ""), message: String(payload.message ?? "") };
    case "plan_generated":
      return {
        type: "plan_generated",
        plan: payload.plan as AnalysisPlan,
        repair_attempts: Number(payload.repair_attempts ?? 0),
      };
    case "tool_executed":
      return {
        type: "tool_executed",
        tool: String(payload.tool ?? ""),
        row_count: Number(payload.row_count ?? 0),
        execution_time_ms: Number(payload.execution_time_ms ?? 0),
        cache_hit: Boolean(payload.cache_hit),
      };
    case "narrative_chunk":
      return { type: "narrative_chunk", text: String(payload.text ?? "") };
    case "chart_ready":
      return { type: "chart_ready", chart: payload.chart as ChartSpec };
    case "run_completed":
      return { type: "run_completed", ...(payload as Omit<Extract<AnalysisEvent, { type: "run_completed" }>, "type">) };
    case "run_failed":
      return {
        type: "run_failed",
        code: String(payload.code ?? "ANALYSIS_FAILED"),
        message: String(payload.message ?? "Analisis gagal."),
        stage: payload.stage ? String(payload.stage) : undefined,
      };
    default:
      return null;
  }
}

export type SSEMessage = { event: string; data: string };

/**
 * Incremental SSE frame parser. Chunks may split anywhere (even mid-frame), so
 * the buffer is kept between calls and only complete `\n\n` blocks are emitted.
 */
export function createSSEParser(): (chunk: string) => SSEMessage[] {
  let buffer = "";
  return (chunk: string) => {
    buffer = (buffer + chunk).replace(/\r\n/g, "\n");
    const out: SSEMessage[] = [];
    for (;;) {
      const end = buffer.indexOf("\n\n");
      if (end < 0) break;
      const block = buffer.slice(0, end);
      buffer = buffer.slice(end + 2);
      let event = "message";
      const data: string[] = [];
      for (const line of block.split("\n")) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) data.push(line.slice(5).replace(/^ /, ""));
      }
      if (data.length > 0) out.push({ event, data: data.join("\n") });
    }
    return out;
  };
}

export const analysisApi = { list: listSessions, create: createSession, get: getSession, stream: streamMessage };