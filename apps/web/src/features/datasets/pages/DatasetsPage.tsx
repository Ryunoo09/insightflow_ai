import { useEffect, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";

import { Button, Card, FieldLabel, FormError, Input } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import type { Dataset } from "../api";
import { datasetsApi } from "../api";
import { StatusBadge } from "../components";

const STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: "", label: "Semua status" },
  { value: "queued", label: "Menunggu" },
  { value: "processing", label: "Memproses" },
  { value: "ready", label: "Siap" },
  { value: "failed", label: "Gagal" },
];

const SORT_OPTIONS: { value: string; label: string; sort: string; order: "asc" | "desc" }[] = [
  { value: "updated_at:desc", label: "Terbaru", sort: "updated_at", order: "desc" },
  { value: "name:asc", label: "Nama A–Z", sort: "name", order: "asc" },
  { value: "name:desc", label: "Nama Z–A", sort: "name", order: "desc" },
  { value: "created_at:desc", label: "Terlama", sort: "created_at", order: "asc" },
];

type Dialog = { kind: "rename" | "delete" | "reprocess"; dataset: Dataset } | null;

/** Server-side dataset list with rename/reprocess/delete actions. */
export function DatasetsPage() {
  const { workspaceId = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();

  const page = Math.max(1, Number(searchParams.get("page")) || 1);
  const q = searchParams.get("q") ?? "";
  const status = searchParams.get("status") ?? "";
  const sort = searchParams.get("sort") ?? "updated_at";
  const order = searchParams.get("order") === "asc" ? "asc" : "desc";

  const [dialog, setDialog] = useState<Dialog>(null);
  const [term, setTerm] = useState(q);

  // Keep the box in sync when the URL changes (back/forward).
  useEffect(() => setTerm(q), [q]);

  function setParams(patch: Record<string, string>, resetPage = true) {
    const next = new URLSearchParams(searchParams);
    for (const [key, value] of Object.entries(patch)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    if (resetPage) next.delete("page");
    setSearchParams(next, { replace: true });
  }

  // Debounce the search box into the URL.
  useEffect(() => {
    if (term === q) return;
    const timer = setTimeout(() => setParams({ q: term }), 300);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [term]);

  const { data, isPending, isError } = useQuery({
    queryKey: ["datasets", workspaceId, { page, q, status, sort, order }],
    queryFn: () => datasetsApi.list(workspaceId, { page, q, status, sort, order, perPage: 20 }),
  });

  const items = data?.data ?? [];
  const meta = data?.meta;
  const totalPages = meta ? Math.max(1, Math.ceil(meta.total / meta.per_page)) : 1;
  const sortValue = `${sort}:${order}`;

  function closeDialog() {
    setDialog(null);
    void queryClient.invalidateQueries({ queryKey: ["datasets", workspaceId] });
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Dataset</h1>
        <div className="flex items-center gap-4">
          <Link
            to={`/app/${workspaceId}/overview`}
            className="text-sm font-medium text-primary hover:underline"
          >
            Ikhtisar
          </Link>
          <Button onClick={() => navigate(`/app/${workspaceId}/datasets/new`)}>Unggah Dataset</Button>
        </div>
      </div>

      <Card>
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-56 flex-1">
            <FieldLabel htmlFor="dataset-search">Cari nama</FieldLabel>
            <Input
              id="dataset-search"
              placeholder="Cari dataset..."
              value={term}
              onChange={(event) => setTerm(event.target.value)}
            />
          </div>
          <div>
            <FieldLabel htmlFor="dataset-status">Status</FieldLabel>
            <select
              id="dataset-status"
              className="min-h-10 w-full rounded-md border border-border bg-surface-raised px-3 text-sm text-foreground"
              value={status}
              onChange={(event) => setParams({ status: event.target.value })}
            >
              {STATUS_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
          <div>
            <FieldLabel htmlFor="dataset-sort">Urutkan</FieldLabel>
            <select
              id="dataset-sort"
              className="min-h-10 w-full rounded-md border border-border bg-surface-raised px-3 text-sm text-foreground"
              value={sortValue}
              onChange={(event) => {
                const option = SORT_OPTIONS.find((item) => item.value === event.target.value);
                if (option) setParams({ sort: option.sort, order: option.order });
              }}
            >
              {SORT_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        {isPending && <p className="mt-4 text-sm text-muted-foreground">Memuat dataset...</p>}
        {isError && <p className="mt-4 text-sm text-danger">Gagal memuat dataset.</p>}
        {!isPending && !isError && items.length === 0 && (
          <p className="mt-4 text-sm text-muted-foreground">
            {q || status ? "Tidak ada dataset yang cocok." : "Belum ada dataset. Unggah yang pertama."}
          </p>
        )}

        {items.length > 0 && (
          <ul className="mt-4 divide-y divide-border">
            {items.map((dataset) => (
              <li key={dataset.id} className="flex flex-wrap items-center gap-3 py-3">
                <Link
                  to={`/app/${workspaceId}/datasets/${dataset.id}`}
                  className="font-medium text-foreground hover:underline"
                >
                  {dataset.name}
                </Link>
                <StatusBadge status={dataset.status} />
                <span className="text-sm text-muted-foreground">
                  {dataset.current_version.row_count ?? "—"} baris ·{" "}
                  {dataset.current_version.column_count ?? "—"} kolom
                </span>
                <span className="text-sm text-muted-foreground">{dataset.file_format.toUpperCase()}</span>
                <div className="ml-auto flex items-center gap-2">
                  <Button variant="ghost" onClick={() => setDialog({ kind: "rename", dataset })}>
                    Ubah nama
                  </Button>
                  <Button variant="ghost" onClick={() => setDialog({ kind: "reprocess", dataset })}>
                    Proses ulang
                  </Button>
                  <Button variant="ghost" onClick={() => setDialog({ kind: "delete", dataset })}>
                    Hapus
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}

        {meta && meta.total > 0 && (
          <div className="mt-4 flex items-center justify-between">
            <p className="text-sm text-muted-foreground">
              Halaman {meta.page} dari {totalPages} · {meta.total} dataset
            </p>
            <div className="flex items-center gap-2">
              <Button
                variant="ghost"
                disabled={page <= 1}
                onClick={() => setParams({ page: String(page - 1) }, false)}
              >
                Sebelumnya
              </Button>
              <Button
                variant="ghost"
                disabled={page >= totalPages}
                onClick={() => setParams({ page: String(page + 1) }, false)}
              >
                Berikutnya
              </Button>
            </div>
          </div>
        )}
      </Card>

      {dialog && <DatasetDialog dialog={dialog} onClose={closeDialog} />}
    </div>
  );
}

function DatasetDialog({ dialog, onClose }: { dialog: NonNullable<Dialog>; onClose: () => void }) {
  const { workspaceId = "" } = useParams();
  const queryClient = useQueryClient();
  const [name, setName] = useState(dialog.dataset.name);
  const [error, setError] = useState<string | null>(null);

  function invalidate() {
    void queryClient.invalidateQueries({ queryKey: ["datasets", workspaceId] });
    void queryClient.invalidateQueries({ queryKey: ["dataset", dialog.dataset.id] });
  }

  const rename = useMutation({
    mutationFn: () => datasetsApi.rename(dialog.dataset.id, name.trim()),
    onSuccess: () => {
      invalidate();
      onClose();
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === "DATASET_NAME_TAKEN") {
        setError("Nama dataset sudah dipakai di ruang kerja ini.");
        return;
      }
      setError(err instanceof ApiError ? err.message : "Gagal mengubah nama.");
    },
  });

  const remove = useMutation({
    mutationFn: () => datasetsApi.remove(dialog.dataset.id),
    onSuccess: () => {
      invalidate();
      onClose();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Gagal menghapus dataset."),
  });

  const reprocess = useMutation({
    mutationFn: () => datasetsApi.reprocess(dialog.dataset.id),
    onSuccess: () => {
      invalidate();
      onClose();
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === "DATASET_BUSY") {
        setError("Dataset sedang diproses. Tunggu sampai selesai lalu coba lagi.");
        return;
      }
      setError(err instanceof ApiError ? err.message : "Gagal memproses ulang dataset.");
    },
  });

  const pending = rename.isPending || remove.isPending || reprocess.isPending;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
      aria-modal="true"
      onClick={onClose}
    >
      <div className="w-full max-w-md rounded-lg border border-border bg-surface-raised p-6" onClick={(event) => event.stopPropagation()}>
        {dialog.kind === "rename" && (
          <>
            <h2 className="text-lg font-semibold">Ubah nama dataset</h2>
            <div className="mt-4 space-y-3">
              <div>
                <FieldLabel htmlFor="rename-input">Nama baru</FieldLabel>
                <Input
                  id="rename-input"
                  maxLength={200}
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </div>
              {error && <FormError>{error}</FormError>}
            </div>
            <DialogActions
              pending={pending}
              confirmLabel="Simpan"
              confirmDisabled={!name.trim()}
              onCancel={onClose}
              onConfirm={() => {
                setError(null);
                rename.mutate();
              }}
            />
          </>
        )}

        {dialog.kind === "delete" && (
          <>
            <h2 className="text-lg font-semibold">Hapus dataset</h2>
            <p className="mt-2 text-sm text-muted-foreground">
              Dataset <span className="text-foreground">{dialog.dataset.name}</span> akan dihapus beserta
              berkasnya setelah masa tenggang. Tindakan ini tidak bisa dibatalkan.
            </p>
            {error && <FormError>{error}</FormError>}
            <DialogActions
              pending={pending}
              confirmLabel="Hapus"
              onCancel={onClose}
              onConfirm={() => {
                setError(null);
                remove.mutate();
              }}
            />
          </>
        )}

        {dialog.kind === "reprocess" && (
          <>
            <h2 className="text-lg font-semibold">Proses ulang dataset</h2>
            <p className="mt-2 text-sm text-muted-foreground">
              Memproses ulang <span className="text-foreground">{dialog.dataset.name}</span> akan membuat versi
              baru dari berkas sumber yang sama. Versi lama tetap tersimpan.
            </p>
            {error && <FormError>{error}</FormError>}
            <DialogActions
              pending={pending}
              confirmLabel="Proses ulang"
              onCancel={onClose}
              onConfirm={() => {
                setError(null);
                reprocess.mutate();
              }}
            />
          </>
        )}
      </div>
    </div>
  );
}

function DialogActions({
  pending,
  confirmLabel,
  confirmDisabled,
  onCancel,
  onConfirm,
}: {
  pending: boolean;
  confirmLabel: string;
  confirmDisabled?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}): ReactNode {
  return (
    <div className="mt-6 flex justify-end gap-2">
      <Button variant="ghost" disabled={pending} onClick={onCancel}>
        Batal
      </Button>
      <Button disabled={pending || confirmDisabled} onClick={onConfirm}>
        {pending ? "Memproses..." : confirmLabel}
      </Button>
    </div>
  );
}
