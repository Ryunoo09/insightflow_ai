import { useMemo, useState, type ReactNode } from "react";

import { cn } from "../../lib/cn";
import { Button } from "../../components/ui";
import type { AnalysisPlan, ChartSpec, ResultEnvelope } from "./api";

/** The four stages the engine reports through `stage_start` (Plan 06 §5). */
export const STAGES: { key: string; label: string }[] = [
  { key: "planning", label: "Menyusun rencana" },
  { key: "computing", label: "Menghitung data" },
  { key: "narrating", label: "Menyusun ringkasan" },
  { key: "done", label: "Selesai" },
];

export type RunStatus = "idle" | "running" | "done" | "failed";

/** Vertical progress list. Reads the current stage name from an SSE frame. */
export function StageIndicator({ stage, status }: { stage: string | null; status: RunStatus }) {
  const current = status === "done" ? STAGES.length : Math.max(0, STAGES.findIndex((s) => s.key === stage));
  return (
    <ol className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm" aria-label="Progres analisis">
      {STAGES.map((item, index) => {
        const done = status === "done" || index < current;
        const active = status === "running" && index === current;
        const failed = status === "failed" && index === current;
        return (
          <li key={item.key} className="flex items-center gap-2">
            <span
              aria-hidden
              className={cn(
                "inline-block h-2.5 w-2.5 rounded-full",
                done && "bg-success",
                active && "animate-pulse bg-primary",
                failed && "bg-danger",
                !done && !active && !failed && "bg-border",
              )}
            />
            <span
              className={cn(
                "text-muted-foreground",
                (done || active) && "text-foreground",
                failed && "text-danger",
              )}
              aria-current={active ? "step" : undefined}
            >
              {item.label}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

/** Accordion exposing the exact plan behind a computed answer (Plan 06 §9 risk 5). */
export function PlanPanel({ plan, repairAttempts }: { plan: AnalysisPlan; repairAttempts?: number }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded-md border border-border bg-surface text-sm">
      <button
        type="button"
        className="flex w-full items-center justify-between px-3 py-2 text-left font-medium"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <span>Rencana perhitungan</span>
        <span className="text-muted-foreground">{open ? "Tutup" : "Lihat"}</span>
      </button>
      {open && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 border-t border-border px-3 py-2">
          <Row label="Tool" value={plan.tool} />
          <Row label="Kolom" value={plan.column} />
          <Row label="Agregasi" value={plan.aggregation} />
          {plan.group_by && <Row label="Kelompok" value={plan.group_by} />}
          {plan.bucket && <Row label="Bucket" value={plan.bucket} />}
          {plan.filters && Object.keys(plan.filters).length > 0 && (
            <Row
              label="Filter"
              value={Object.entries(plan.filters)
                .map(([key, value]) => `${key} = ${value}`)
                .join(", ")}
            />
          )}
          <Row label="Maksud" value={plan.intent} />
          {repairAttempts !== undefined && repairAttempts > 1 && (
            <Row label="Perbaikan" value={`${repairAttempts - 1}x retry`} />
          )}
        </dl>
      )}
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="break-words text-foreground">{value}</dd>
    </>
  );
}

/** Popover with the aggregate rows the narrative was derived from. */
export function EvidenceViewer({ result }: { result: ResultEnvelope }) {
  const [open, setOpen] = useState(false);
  const columns = result.columns.map((column) => column.name);
  return (
    <div className="rounded-md border border-border bg-surface text-sm">
      <button
        type="button"
        className="flex w-full items-center justify-between px-3 py-2 text-left font-medium"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <span>Bukti data ({result.row_count} baris)</span>
        <span className="text-muted-foreground">{open ? "Tutup" : "Lihat"}</span>
      </button>
      {open && (
        <div className="max-h-72 overflow-auto border-t border-border">
          <table className="w-full border-collapse text-xs">
            <thead className="sticky top-0 bg-surface-raised">
              <tr>
                {columns.map((name) => (
                  <th key={name} scope="col" className="border-b border-border px-3 py-2 text-left font-medium">
                    {name}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {result.rows.map((row, index) => (
                <tr key={index}>
                  {columns.map((name) => (
                    <td key={name} className="border-b border-border px-3 py-1.5 tabular-nums">
                      {formatCell(row[name])}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function formatCell(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "number") return value.toLocaleString("id-ID", { maximumFractionDigits: 4 });
  return String(value);
}

const CHART_WIDTH = 480;
const CHART_HEIGHT = 220;
const PADDING = 32;

/** Deterministic chart renderer. The engine picks `kind`; this only draws it. */
export function ChartCard({ chart, result }: { chart: ChartSpec; result: ResultEnvelope }) {
  const points = useMemo(() => chartPoints(chart, result), [chart, result]);
  if (chart.kind === "table" || points.length === 0) return null;
  return (
    <figure className="rounded-md border border-border bg-surface p-3">
      <figcaption className="mb-2 text-sm font-medium">{chart.title}</figcaption>
      {renderChart(chart, points)}
      <p className="mt-2 text-xs text-muted-foreground">{chart.reason}</p>
    </figure>
  );
}

type Point = { label: string; x: number; y: number };

function chartPoints(chart: ChartSpec, result: ResultEnvelope): Point[] {
  const xKey = chart.x ?? result.columns[0]?.name;
  const yKey = chart.y ?? result.columns[result.columns.length - 1]?.name;
  if (!xKey || !yKey) return [];
  const out: Point[] = [];
  for (const row of result.rows) {
    const y = Number(row[yKey]);
    if (!Number.isFinite(y)) continue;
    const rawX = row[xKey];
    out.push({ label: String(rawX ?? ""), x: Number.isFinite(Number(rawX)) ? Number(rawX) : out.length, y });
  }
  return out;
}

function renderChart(chart: ChartSpec, points: Point[]): ReactNode {
  if (chart.kind === "pie") return <PieChart points={points} />;

  const xs = points.map((point) => point.x);
  const ys = points.map((point) => point.y);
  const xMin = Math.min(...xs);
  const xMax = Math.max(...xs);
  const yMin = Math.min(0, ...ys);
  const yMax = Math.max(...ys);
  const sx = (value: number) => PADDING + ((value - xMin) / (xMax - xMin || 1)) * (CHART_WIDTH - PADDING * 2);
  const sy = (value: number) => CHART_HEIGHT - PADDING - ((value - yMin) / (yMax - yMin || 1)) * (CHART_HEIGHT - PADDING * 2);

  const axis = (
    <>
      <line x1={PADDING} y1={CHART_HEIGHT - PADDING} x2={CHART_WIDTH - PADDING} y2={CHART_HEIGHT - PADDING} stroke="currentColor" className="text-border" />
      <line x1={PADDING} y1={PADDING} x2={PADDING} y2={CHART_HEIGHT - PADDING} stroke="currentColor" className="text-border" />
    </>
  );

  if (chart.kind === "scatter") {
    return (
      <svg viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`} className="h-auto w-full text-foreground" role="img" aria-label={chart.title}>
        {axis}
        {points.map((point, index) => (
          <circle key={index} cx={sx(point.x)} cy={sy(point.y)} r={3} className="fill-primary" />
        ))}
      </svg>
    );
  }

  if (chart.kind === "line") {
    const path = points.map((point, index) => `${index === 0 ? "M" : "L"}${sx(point.x)},${sy(point.y)}`).join(" ");
    return (
      <svg viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`} className="h-auto w-full text-foreground" role="img" aria-label={chart.title}>
        {axis}
        <path d={path} fill="none" strokeWidth={2} className="stroke-primary" />
      </svg>
    );
  }

  // bar: category index on x, value on y
  const band = (CHART_WIDTH - PADDING * 2) / points.length;
  return (
    <svg viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`} className="h-auto w-full text-foreground" role="img" aria-label={chart.title}>
      {axis}
      {points.map((point, index) => {
        const top = sy(point.y);
        const bottom = sy(yMin);
        return (
          <rect
            key={index}
            x={PADDING + index * band + band * 0.15}
            y={top}
            width={band * 0.7}
            height={Math.max(1, bottom - top)}
            className="fill-primary"
          >
            <title>{`${point.label}: ${point.y}`}</title>
          </rect>
        );
      })}
    </svg>
  );
}

function PieChart({ points }: { points: Point[] }) {
  const total = points.reduce((sum, point) => sum + Math.max(0, point.y), 0) || 1;
  const radius = 70;
  const center = 90;
  let angle = -Math.PI / 2;
  const slices = points.map((point, index) => {
    const share = Math.max(0, point.y) / total;
    const start = angle;
    const end = angle + share * Math.PI * 2;
    angle = end;
    const large = end - start > Math.PI ? 1 : 0;
    const x1 = center + radius * Math.cos(start);
    const y1 = center + radius * Math.sin(start);
    const x2 = center + radius * Math.cos(end);
    const y2 = center + radius * Math.sin(end);
    const hue = (index * 360) / Math.max(1, points.length);
    return { path: `M${center},${center} L${x1},${y1} A${radius},${radius} 0 ${large} 1 ${x2},${y2} Z`, hue, point };
  });
  return (
    <svg viewBox="0 0 180 180" className="h-44 w-44" role="img" aria-label="Diagram lingkaran">
      {slices.map((slice, index) => (
        <path key={index} d={slice.path} fill={`hsl(${slice.hue} 70% 55%)`}>
          <title>{`${slice.point.label}: ${slice.point.y}`}</title>
        </path>
      ))}
    </svg>
  );
}

export type ChatMessage = {
  id: string;
  role: "user" | "assistant" | "system";
  content: string;
  result?: ResultEnvelope;
  chart?: ChartSpec;
  plan?: AnalysisPlan;
  repairAttempts?: number;
  grounded?: boolean;
  streaming?: boolean;
};

/** One conversation turn. Assistant turns may carry plan, chart, and evidence. */
export function MessageItem({ message }: { message: ChatMessage }) {
  const isUser = message.role === "user";
  return (
    <div className={cn("flex", isUser ? "justify-end" : "justify-start")}>
      <div className={cn("max-w-[42rem] space-y-2", isUser ? "items-end" : "items-start")}>
        <div
          className={cn(
            "rounded-lg border px-4 py-3 text-sm",
            isUser ? "border-primary bg-primary text-primary-foreground" : "border-border bg-surface-raised text-foreground",
          )}
        >
          <p className="whitespace-pre-wrap break-words">{message.content}</p>
        </div>
        {!isUser && message.plan && <PlanPanel plan={message.plan} repairAttempts={message.repairAttempts} />}
        {!isUser && message.chart && message.result && <ChartCard chart={message.chart} result={message.result} />}
        {!isUser && message.result && <EvidenceViewer result={message.result} />}
        {!isUser && message.result && (
          <div className="flex items-center gap-2">
            <Button variant="ghost" disabled title="Tersedia pada plan dashboard">
              Simpan ke Dashboard
            </Button>
            {message.grounded === false && (
              <span className="text-xs text-muted-foreground">
                Ringkasan tidak lolos pemeriksaan angka; lihat bukti data.
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
