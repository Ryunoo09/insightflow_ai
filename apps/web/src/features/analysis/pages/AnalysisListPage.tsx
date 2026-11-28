import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";

import { Button, Card, FieldLabel, FormError, Input } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { datasetsApi } from "../../datasets/api";
import { analysisApi } from "../api";

/** Lists a workspace's analysis sessions and starts a new one. */
export function AnalysisListPage() {
  const { workspaceId = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [datasetId, setDatasetId] = useState("");
  const [title, setTitle] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const sessions = useQuery({
    queryKey: ["analysis-sessions", workspaceId],
    queryFn: () => analysisApi.list(workspaceId),
    enabled: Boolean(workspaceId),
  });

  const datasets = useQuery({
    queryKey: ["datasets", workspaceId, "ready"],
    queryFn: () => datasetsApi.list(workspaceId, { status: "ready", perPage: 100 }),
    enabled: Boolean(workspaceId),
  });

  async function onCreate(event: React.FormEvent) {
    event.preventDefault();
    if (!datasetId) {
      setError("Pilih dataset yang akan dianalisis.");
      return;
    }
    setCreating(true);
    setError(null);
    try {
      const session = await analysisApi.create(workspaceId, datasetId, title);
      await queryClient.invalidateQueries({ queryKey: ["analysis-sessions", workspaceId] });
      navigate(`/app/${workspaceId}/analysis/${session.id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Gagal membuat sesi analisis.");
    } finally {
      setCreating(false);
    }
  }

  const ready = datasets.data?.data ?? [];

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">Analisis AI</h1>

      <Card>
        <h2 className="text-lg font-semibold">Sesi baru</h2>
        <form onSubmit={onCreate} className="mt-3 space-y-4">
          <div>
            <FieldLabel htmlFor="analysis-dataset">Dataset</FieldLabel>
            <select
              id="analysis-dataset"
              value={datasetId}
              onChange={(event) => setDatasetId(event.target.value)}
              className="min-h-10 w-full rounded-md border border-border bg-surface-raised px-3 text-sm text-foreground"
            >
              <option value="">Pilih dataset...</option>
              {ready.map((dataset) => (
                <option key={dataset.id} value={dataset.id}>
                  {dataset.name}
                </option>
              ))}
            </select>
            {ready.length === 0 && !datasets.isPending && (
              <p className="mt-1 text-xs text-muted-foreground">
                Belum ada dataset siap. Unggah dataset terlebih dahulu.
              </p>
            )}
          </div>
          <div>
            <FieldLabel htmlFor="analysis-title">Judul sesi (opsional)</FieldLabel>
            <Input
              id="analysis-title"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              maxLength={200}
              placeholder="Analisis penjualan"
            />
          </div>
          {error && <FormError>{error}</FormError>}
          <Button type="submit" disabled={creating}>
            {creating ? "Membuat..." : "Mulai analisis"}
          </Button>
        </form>
      </Card>

      <section className="space-y-3">
        <h2 className="text-lg font-semibold">Riwayat sesi</h2>
        {sessions.isPending && <p className="text-sm text-muted-foreground">Memuat...</p>}
        {sessions.data?.data.length === 0 && (
          <p className="text-sm text-muted-foreground">Belum ada sesi analisis.</p>
        )}
        <ul className="space-y-2">
          {sessions.data?.data.map((session) => (
            <li key={session.id}>
              <Link
                to={`/app/${workspaceId}/analysis/${session.id}`}
                className="flex items-center justify-between rounded-md border border-border bg-surface-raised px-4 py-3 text-sm hover:bg-surface"
              >
                <span className="font-medium text-foreground">{session.title}</span>
                <span className="text-xs text-muted-foreground">
                  {new Date(session.updated_at).toLocaleString("id-ID")}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
