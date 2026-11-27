import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";

import { Button, Card, FormError } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { analysisApi, type AnalysisEvent, type AnalysisPlan } from "../api";
import { MessageItem, StageIndicator, type ChatMessage, type RunStatus } from "../components";

/** Chat page for one analysis session (/app/:workspaceId/analysis/:sessionId). */
export function AnalysisPage() {
  const { workspaceId = "", sessionId = "" } = useParams();
  const [turns, setTurns] = useState<ChatMessage[]>([]);
  const [question, setQuestion] = useState("");
  const [stage, setStage] = useState<string | null>(null);
  const [status, setStatus] = useState<RunStatus>("idle");
  const [streamText, setStreamText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const seeded = useRef(false);

  const { data, isPending } = useQuery({
    queryKey: ["analysis-session", sessionId],
    queryFn: () => analysisApi.get(sessionId),
    enabled: Boolean(sessionId),
  });

  // Seed the transcript once; later runs append locally so the streamed assistant
  // turn is never duplicated by a refetch.
  useEffect(() => {
    if (!data || seeded.current) return;
    seeded.current = true;
    setTurns(
      data.messages.map((message) => ({
        id: message.id,
        role: message.role === "user" ? "user" : "assistant",
        content: message.content,
      })),
    );
  }, [data]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: "end" });
  }, [turns, streamText, status]);

  async function onSubmit(event: React.FormEvent) {
    event.preventDefault();
    const content = question.trim();
    if (!content || status === "running") return;
    setQuestion("");
    setError(null);
    setTurns((current) => [...current, { id: `u-${Date.now()}`, role: "user", content }]);
    setStage("planning");
    setStatus("running");
    setStreamText("");

    // Values captured across frames, then attached to the final turn.
    let plan: AnalysisPlan | undefined;
    let repairAttempts: number | undefined;
    let narrative = "";
    try {
      await analysisApi.stream(sessionId, content, (frame: AnalysisEvent) => {
        switch (frame.type) {
          case "stage_start":
            setStage(frame.stage);
            break;
          case "plan_generated":
            plan = frame.plan;
            repairAttempts = frame.repair_attempts;
            break;
          case "narrative_chunk":
            narrative += frame.text;
            setStreamText(narrative);
            break;
          case "run_completed":
            narrative = frame.narrative;
            setStreamText("");
            setStage("done");
            setStatus("done");
            setTurns((current) => [
              ...current,
              {
                id: `a-${Date.now()}`,
                role: "assistant",
                content: frame.narrative,
                result: frame.result,
                chart: frame.chart,
                plan,
                repairAttempts,
                grounded: frame.narrative_grounded,
              },
            ]);
            break;
          case "run_failed":
            throw new ApiError(502, frame.code, frame.message);
        }
      });
      // A stream that ends without run_completed still has to leave a trace.
      setStatus((current) => (current === "running" ? "failed" : current));
    } catch (err) {
      setStatus("failed");
      setStreamText("");
      setError(err instanceof ApiError ? err.message : "Analisis gagal. Coba lagi.");
    }
  }

  if (isPending) return <p className="text-sm text-muted-foreground">Memuat...</p>;

  return (
    <div className="flex min-h-[70vh] flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">{data?.title ?? "Analisis"}</h1>
        <Link to={`/app/${workspaceId}/datasets/${data?.dataset_id ?? ""}`} className="text-sm font-medium text-primary hover:underline">
          Dataset
        </Link>
        <Link to={`/app/${workspaceId}/analysis`} className="ml-auto text-sm font-medium text-primary hover:underline">
          Semua sesi
        </Link>
      </div>

      {status !== "idle" && (
        <div className="rounded-md border border-border bg-surface-raised p-3">
          <StageIndicator stage={stage} status={status} />
        </div>
      )}

      <div className="flex-1 space-y-4">
        {turns.length === 0 && status === "idle" && (
          <Card>
            <p className="text-sm text-muted-foreground">
              Ajukan pertanyaan tentang dataset ini, misalnya "berapa rata-rata penjualan per wilayah?".
            </p>
          </Card>
        )}
        {turns.map((message) => (
          <MessageItem key={message.id} message={message} />
        ))}
        {status === "running" && streamText && (
          <div aria-live="polite">
            <MessageItem message={{ id: "streaming", role: "assistant", content: streamText, streaming: true }} />
          </div>
        )}
        {status === "running" && !streamText && (
          <p aria-live="polite" className="text-sm text-muted-foreground">
            Memproses pertanyaan...
          </p>
        )}
        <div ref={bottomRef} />
      </div>

      {error && <FormError>{error}</FormError>}

      <form onSubmit={onSubmit} className="sticky bottom-4 flex gap-2 rounded-lg border border-border bg-surface-raised p-3">
        <label htmlFor="analysis-question" className="sr-only">
          Pertanyaan
        </label>
        <input
          id="analysis-question"
          value={question}
          onChange={(event) => setQuestion(event.target.value)}
          disabled={status === "running"}
          maxLength={2000}
          placeholder="Tanya sesuatu tentang data ini..."
          className="min-h-10 w-full rounded-md border border-border bg-surface px-3 text-sm text-foreground placeholder:text-muted-foreground disabled:opacity-60"
        />
        <Button type="submit" disabled={status === "running" || question.trim() === ""}>
          {status === "running" ? "..." : "Kirim"}
        </Button>
      </form>
    </div>
  );
}
