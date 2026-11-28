import { lazy, Suspense, useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { Card, FieldLabel, Input } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { datasetsApi, type ExplorerSort } from "../api";
import { StatusBadge } from "../components";

// The grid is heavy; load it only on the explorer route.
const DataExplorer = lazy(() => import("../DataExplorer").then((module) => ({ default: module.DataExplorer })));

/** Data explorer page: full-width virtualized grid at /app/:workspaceId/datasets/:datasetId/explorer. */
export function DatasetExplorerPage() {
  const { workspaceId = "", datasetId = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();

  const q = searchParams.get("q") ?? "";
  const version = Number(searchParams.get("version")) || 0;
  const sortColumn = searchParams.get("sort") ?? "";
  const sortOrder = searchParams.get("order") === "asc" ? "asc" : "desc";
  const initialSort: ExplorerSort | null = sortColumn ? { column: sortColumn, direction: sortOrder } : null;

  const [term, setTerm] = useState(q);
  useEffect(() => setTerm(q), [q]);

  // Debounce the quick search into the URL so it stays shareable.
  useEffect(() => {
    if (term === q) return;
    const timer = setTimeout(() => {
      const next = new URLSearchParams(searchParams);
      if (term.trim()) next.set("q", term.trim());
      else next.delete("q");
      setSearchParams(next, { replace: true });
    }, 300);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [term]);

  const versions = useQuery({
    queryKey: ["dataset-versions", datasetId],
    queryFn: () => datasetsApi.versions(datasetId),
  });
  const currentVersion = versions.data?.data.reduce((max, item) => Math.max(max, item.version_no), 0) ?? 0;
  const selected = version || currentVersion;

  const schema = useQuery({
    queryKey: ["explorer-columns", datasetId, selected],
    queryFn: () => datasetsApi.rows(datasetId, { offset: 0, limit: 1, versionNo: selected || undefined }),
    staleTime: Infinity,
  });

  const selectedVersion = versions.data?.data.find((item) => item.version_no === selected);
  const notReady = schema.error instanceof ApiError && schema.error.code === "DATASET_NOT_READY";

  function onSortChange(sort: ExplorerSort | null) {
    const next = new URLSearchParams(searchParams);
    if (sort) {
      next.set("sort", sort.column);
      next.set("order", sort.direction);
    } else {
      next.delete("sort");
      next.delete("order");
    }
    setSearchParams(next, { replace: true });
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex-1">
          <h1 className="text-2xl font-semibold">Data Explorer</h1>
          <Link
            to={`/app/${workspaceId}/datasets/${datasetId}`}
            className="text-sm font-medium text-primary hover:underline"
          >
            Kembali ke dataset
          </Link>
        </div>
        {versions.data && versions.data.data.length > 1 && (
          <div>
            <FieldLabel htmlFor="explorer-version">Versi</FieldLabel>
            <select
              id="explorer-version"
              className="min-h-10 w-full rounded-md border border-border bg-surface-raised px-3 text-sm text-foreground"
              value={String(selected)}
              onChange={(event) => {
                const next = new URLSearchParams(searchParams);
                next.set("version", event.target.value);
                setSearchParams(next, { replace: true });
              }}
            >
              {versions.data.data
                .slice()
                .sort((a, b) => b.version_no - a.version_no)
                .map((item) => (
                  <option key={item.version_no} value={item.version_no}>
                    v{item.version_no} · {item.status}
                    {item.version_no === currentVersion ? " (aktif)" : ""}
                  </option>
                ))}
            </select>
          </div>
        )}
        <div className="hidden min-w-56 md:block">
          <FieldLabel htmlFor="explorer-search">Cari di semua kolom</FieldLabel>
          <Input
            id="explorer-search"
            placeholder="Cari nilai..."
            value={term}
            disabled={notReady}
            onChange={(event) => setTerm(event.target.value)}
          />
        </div>
      </div>

      {schema.data && (
        <div className="flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
          <StatusBadge status={selectedVersion?.status ?? "ready"} />
          <span>{schema.data.total.toLocaleString("id-ID")} baris</span>
          <span>· {schema.data.columns.length} kolom</span>
        </div>
      )}

      {schema.isPending && <p className="text-sm text-muted-foreground">Memuat data explorer...</p>}

      {schema.error && (
        <Card className="space-y-2">
          <h2 className="text-lg font-semibold">
            {notReady ? "Versi ini belum siap" : "Gagal memuat data explorer"}
          </h2>
          <p className="text-sm text-muted-foreground">
            {notReady
              ? "Pilih versi lain yang sudah selesai diproses."
              : schema.error instanceof ApiError
                ? schema.error.message
                : "Terjadi kesalahan saat memuat data."}
          </p>
        </Card>
      )}

      {schema.data && !notReady && (
        <Suspense fallback={<p className="text-sm text-muted-foreground">Memuat tabel...</p>}>
          <DataExplorer
            key={`${datasetId}:${selected}`}
            datasetId={datasetId}
            columns={schema.data.columns}
            version={selected}
            search={q}
            initialSort={initialSort}
            onSortChange={onSortChange}
          />
        </Suspense>
      )}
    </div>
  );
}