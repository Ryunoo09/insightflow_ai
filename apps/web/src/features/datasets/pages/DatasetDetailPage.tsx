import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";

import { Button, Card, FormError } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { datasetsApi } from "../api";
import { QualityScoreCard, StatusBadge } from "../components";

/** Dataset detail with live status polling (/app/:workspaceId/datasets/:datasetId). */
export function DatasetDetailPage() {
  const { workspaceId = "", datasetId = "" } = useParams();
  const queryClient = useQueryClient();
  const [actionError, setActionError] = useState<string | null>(null);

  const { data, isPending, error } = useQuery({
    queryKey: ["dataset", datasetId],
    queryFn: () => datasetsApi.get(datasetId),
    // Keep polling until the pipeline reaches a terminal state.
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "queued" || status === "processing" ? 2000 : false;
    },
  });

  const busy = data?.status === "queued" || data?.status === "processing";

  const reprocess = useMutation({
    mutationFn: () => datasetsApi.reprocess(datasetId),
    onSuccess: () => {
      setActionError(null);
      void queryClient.invalidateQueries({ queryKey: ["dataset", datasetId] });
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === "DATASET_BUSY") {
        setActionError("Dataset sedang diproses. Tunggu sampai selesai lalu coba lagi.");
        return;
      }
      setActionError(err instanceof ApiError ? err.message : "Gagal memproses ulang dataset.");
    },
  });

  if (isPending) {
    return <p className="text-sm text-muted-foreground">Memuat...</p>;
  }

  if (error) {
    const notFound = error instanceof ApiError && error.status === 404;
    return (
      <Card className="space-y-2">
        <h1 className="text-xl font-semibold">Dataset tidak tersedia</h1>
        <p className="text-sm text-muted-foreground">
          {notFound ? "Dataset tidak ditemukan atau kamu tidak punya akses." : "Gagal memuat dataset."}
        </p>
        <Link to={`/app/${workspaceId}/datasets`} className="text-sm font-medium text-primary hover:underline">
          Kembali ke daftar dataset
        </Link>
      </Card>
    );
  }

  const version = data.current_version;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-semibold">{data.name}</h1>
        <StatusBadge status={data.status} />
        <span className="rounded-full border border-border px-2 py-0.5 text-xs font-medium text-muted-foreground">
          v{version.version_no}
        </span>
        <span className="text-sm text-muted-foreground">{data.file_format.toUpperCase()}</span>
        <Button
          variant="ghost"
          className="ml-auto"
          disabled={busy || reprocess.isPending}
          onClick={() => {
            setActionError(null);
            reprocess.mutate();
          }}
        >
          {reprocess.isPending ? "Memproses..." : "Proses ulang"}
        </Button>
        <Link
          to={`/app/${workspaceId}/datasets/${datasetId}/explorer`}
          className="text-sm font-medium text-primary hover:underline"
        >
          Data Explorer
        </Link>
        <Link
          to={`/app/${workspaceId}/datasets`}
          className="text-sm font-medium text-primary hover:underline"
        >
          Semua dataset
        </Link>
      </div>

      {actionError && <FormError>{actionError}</FormError>}

      {data.status === "failed" && version.error && (
        <Card className="border-danger">
          <h2 className="text-lg font-semibold text-danger">Pemrosesan gagal</h2>
          <p className="mt-1 text-sm text-muted-foreground">{version.error.message}</p>
          <p className="mt-1 text-xs text-muted-foreground">Kode: {version.error.code}</p>
        </Card>
      )}

      <div className="grid gap-4 sm:grid-cols-3">
        <QualityScoreCard score={data.quality_score} />
        <Card>
          <p className="text-sm text-muted-foreground">Baris</p>
          <p className="mt-1 text-3xl font-semibold text-foreground">{version.row_count ?? "—"}</p>
        </Card>
        <Card>
          <p className="text-sm text-muted-foreground">Kolom</p>
          <p className="mt-1 text-3xl font-semibold text-foreground">{version.column_count ?? "—"}</p>
        </Card>
      </div>

      <Card>
        <h2 className="text-lg font-semibold">Detail berkas</h2>
        <dl className="mt-3 grid gap-2 text-sm sm:grid-cols-2">
          <Detail label="Nama berkas asli" value={data.original_filename} />
          <Detail label="Versi" value={`v${version.version_no}`} />
          <Detail label="Ukuran sumber" value={formatBytes(version.source_size_bytes)} />
          <Detail label="Diunggah" value={new Date(data.created_at).toLocaleString("id-ID")} />
        </dl>
        {(data.status === "queued" || data.status === "processing") && (
          <p className="mt-4 text-sm text-muted-foreground">
            Sedang diproses. Halaman ini memperbarui otomatis.
          </p>
        )}
      </Card>
    </div>
  );
}

function Detail({ label, value }: { label: string; value: string | number }) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-foreground">{value}</dd>
    </div>
  );
}

function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}
