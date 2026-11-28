import { afterEach, describe, expect, it, vi } from "vitest";

import { useAuthStore } from "../auth/store";
import { createSSEParser, streamMessage, toAnalysisEvent } from "./api";

afterEach(() => {
  vi.restoreAllMocks();
  useAuthStore.setState({ accessToken: null, user: null });
});

describe("createSSEParser", () => {
  it("emits a frame only once its terminator arrives", () => {
    const push = createSSEParser();
    expect(push("event: stage_start\ndata: {\"stage\":\"planning\"}")).toEqual([]);
    const frames = push("\n\n");
    expect(frames).toEqual([{ event: "stage_start", data: '{"stage":"planning"}' }]);
  });

  it("reassembles frames split across chunk boundaries", () => {
    const push = createSSEParser();
    const first = push("event: narrative_chunk\ndata: {\"text\":\"ha");
    expect(first).toEqual([]);
    const frames = push("lo\"}\n\nevent: chart_ready\ndata: {\"chart\":{}}\n\n");
    expect(frames).toHaveLength(2);
    expect(frames[0]).toEqual({ event: "narrative_chunk", data: '{"text":"halo"}' });
    expect(frames[1].event).toBe("chart_ready");
  });
});

describe("toAnalysisEvent", () => {
  it("maps a completed run and ignores unknown events", () => {
    expect(toAnalysisEvent({ event: "ping", data: "{}" })).toBeNull();
    const event = toAnalysisEvent({
      event: "run_completed",
      data: '{"narrative":"ok","narrative_grounded":true,"result":{"tool":"aggregate_metric"}}',
    });
    expect(event?.type).toBe("run_completed");
  });

  it("survives malformed JSON without throwing", () => {
    expect(toAnalysisEvent({ event: "stage_start", data: "not json" })).toBeNull();
  });
});

describe("streamMessage", () => {
  it("reads frames incrementally and resolves when the body ends", async () => {
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('event: stage_start\ndata: {"stage":"planning"}\n\n'));
        controller.enqueue(encoder.encode('event: run_completed\ndata: {"narrative":"selesai"}\n\n'));
        controller.close();
      },
    });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(body, { status: 200 }));

    const seen: string[] = [];
    await streamMessage("ses-1", "berapa rata-rata?", (event) => seen.push(event.type));
    expect(seen).toEqual(["stage_start", "run_completed"]);
  });

  it("throws ApiError when the server rejects before streaming", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "ANALYSIS_QUOTA_EXCEEDED", message: "habis" } }), {
        status: 429,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await expect(streamMessage("ses-1", "q", () => {})).rejects.toMatchObject({
      code: "ANALYSIS_QUOTA_EXCEEDED",
    });
  });
});
