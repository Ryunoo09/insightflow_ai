import { useRef, useState, type DragEvent } from "react";

import { cn } from "../../lib/cn";
import { Button } from "../../components/ui";

const STATUS_LABEL: Record<string, string> = {
  queued: "Menunggu",
  processing: "Memproses",
  ready: "Siap",
  failed: "Gagal",
};

const STATUS_STYLE: Record<string, string> = {
  queued: "border-border text-muted-foreground",
  processing: "border-info text-info",
  ready: "border-success text-success",
  failed: "border-danger text-danger",
};

export function StatusBadge({ status }: { status: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium",
        STATUS_STYLE[status] ?? STATUS_STYLE.queued,
      )}
    >
      {STATUS_LABEL[status] ?? status}
    </span>
  );
}

export function ProgressBar({ percent }: { percent: number }) {
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-surface" role="progressbar" aria-valuenow={percent}>
      <div className="h-full bg-primary transition-all" style={{ width: `${percent}%` }} />
    </div>
  );
}

export function QualityScoreCard({ score }: { score: number | undefined }) {
  return (
    <div className="rounded-lg border border-border bg-surface-raised p-4">
      <p className="text-sm text-muted-foreground">Skor kualitas</p>
      <p className="mt-1 text-3xl font-semibold text-foreground">
        {score === undefined ? "—" : Math.round(score)}
        <span className="text-base text-muted-foreground"> / 100</span>
      </p>
    </div>
  );
}

type DropzoneProps = {
  onFile: (file: File) => void;
  disabled?: boolean;
};

/** Drag-and-drop (or click) picker restricted to CSV and XLSX. */
export function Dropzone({ onFile, disabled }: DropzoneProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);

  function handleDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setDragging(false);
    const file = event.dataTransfer.files?.[0];
    if (file) onFile(file);
  }

  return (
    <div
      onDragOver={(event) => {
        event.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={handleDrop}
      className={cn(
        "flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed p-8 text-center",
        dragging ? "border-primary bg-surface" : "border-border bg-surface-raised",
      )}
    >
      <p className="text-sm text-muted-foreground">
        Seret file ke sini, atau pilih dari komputer.
      </p>
      <p className="text-xs text-muted-foreground">CSV (maks. 50 MB) atau XLSX (maks. 20 MB)</p>
      <Button variant="ghost" disabled={disabled} onClick={() => inputRef.current?.click()}>
        Pilih file
      </Button>
      <input
        ref={inputRef}
        type="file"
        accept=".csv,.xlsx"
        className="hidden"
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) onFile(file);
          event.target.value = "";
        }}
      />
    </div>
  );
}
