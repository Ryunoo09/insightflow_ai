import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";

import { Button, Card, FieldLabel, FormError, Input } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { datasetsApi, fetchLimits, UPLOAD_LIMITS } from "../api";
import { Dropzone, ProgressBar } from "../components";

/** Dedicated upload page (/app/:workspaceId/datasets/new). */
export function DatasetNewPage() {
  const { workspaceId = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [name, setName] = useState("");
  const [percent, setPercent] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const limits = useQuery({ queryKey: ["config", "limits"], queryFn: fetchLimits, staleTime: Infinity });

  const upload = useMutation({
    mutationFn: () => datasetsApi.upload(workspaceId, file as File, name, setPercent),
    onSuccess: (dataset) => {
      void queryClient.invalidateQueries({ queryKey: ["datasets", workspaceId] });
      navigate(`/app/${workspaceId}/datasets/${dataset.id}`);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Unggah gagal."),
  });

  const csvBytes = limits.data?.upload.csv_max_bytes ?? UPLOAD_LIMITS.csvBytes;
  const xlsxBytes = limits.data?.upload.xlsx_max_bytes ?? UPLOAD_LIMITS.xlsxBytes;

  function pick(selected: File) {
    setError(null);
    setPercent(0);
    const ext = selected.name.toLowerCase().split(".").pop();
    if (ext !== "csv" && ext !== "xlsx") {
      setFile(null);
      setError("Hanya file CSV atau XLSX yang didukung.");
      return;
    }
    const limit = ext === "csv" ? csvBytes : xlsxBytes;
    if (selected.size > limit) {
      setFile(null);
      setError(`Ukuran file melebihi batas ${Math.round(limit / 1024 / 1024)} MB.`);
      return;
    }
    setFile(selected);
    if (!name) setName(selected.name.replace(/\.[^.]+$/, ""));
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Unggah dataset</h1>
        <Link
          to={`/app/${workspaceId}/datasets`}
          className="text-sm font-medium text-primary hover:underline"
        >
          Semua dataset
        </Link>
      </div>

      <Card>
        <div className="space-y-4">
          <Dropzone onFile={pick} disabled={upload.isPending} />

          <div className="text-xs text-muted-foreground">
            <p>
              CSV maks. {formatMb(csvBytes)} · XLSX maks. {formatMb(xlsxBytes)}
            </p>
            {limits.data && (
              <p className="mt-1">
                Maks. {limits.data.dataset.max_columns} kolom ·{" "}
                {limits.data.dataset.max_rows_csv.toLocaleString("id-ID")} baris (CSV) ·{" "}
                {limits.data.dataset.max_rows_xlsx.toLocaleString("id-ID")} baris (XLSX)
              </p>
            )}
          </div>

          {file && (
            <p className="text-sm text-muted-foreground">
              Terpilih: <span className="text-foreground">{file.name}</span> ({Math.round(file.size / 1024)} KB)
            </p>
          )}

          <div>
            <FieldLabel htmlFor="dataset-name">Nama dataset</FieldLabel>
            <Input
              id="dataset-name"
              maxLength={200}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </div>

          {upload.isPending && <ProgressBar percent={percent} />}
          {error && <FormError>{error}</FormError>}

          <Button
            disabled={!file || upload.isPending}
            onClick={() => {
              setError(null);
              upload.mutate();
            }}
          >
            {upload.isPending ? `Mengunggah ${percent}%` : "Unggah"}
          </Button>
        </div>
      </Card>
    </div>
  );
}

function formatMb(bytes: number): string {
  return `${Math.round(bytes / 1024 / 1024)} MB`;
}
