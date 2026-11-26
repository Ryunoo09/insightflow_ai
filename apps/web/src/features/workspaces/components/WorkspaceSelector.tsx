import { useQuery } from "@tanstack/react-query";

import { workspacesApi, type Workspace } from "../api";

type Props = {
  value: string | null;
  onChange: (workspace: Workspace) => void;
  /** Visible label text; null renders a screen-reader-only label (header use). */
  label?: string | null;
};

/** Lists the caller's workspaces; picking one drives the active tenant. */
export function WorkspaceSelector({ value, onChange, label = "Ruang kerja" }: Props) {
  const { data, isPending, isError } = useQuery({
    queryKey: ["workspaces"],
    queryFn: workspacesApi.list,
  });

  if (isPending) {
    return <p className="text-sm text-muted-foreground">Memuat ruang kerja...</p>;
  }
  if (isError) {
    return <p className="text-sm text-danger">Gagal memuat ruang kerja.</p>;
  }

  const items = data?.data ?? [];
  if (items.length === 0) {
    return <p className="text-sm text-muted-foreground">Belum ada ruang kerja.</p>;
  }

  return (
    <label className="block text-sm">
      <span className={label === null ? "sr-only" : "mb-1 block font-medium text-foreground"}>
        {label ?? "Ruang kerja"}
      </span>
      <select
        className="min-h-10 w-full rounded-md border border-border bg-surface-raised px-3 text-sm text-foreground"
        value={value ?? ""}
        onChange={(e) => {
          const picked = items.find((w) => w.id === e.target.value);
          if (picked) onChange(picked);
        }}
      >
        {value === null && (
          <option value="" disabled>
            Pilih ruang kerja
          </option>
        )}
        {items.map((ws) => (
          <option key={ws.id} value={ws.id}>
            {ws.name} ({ws.role})
          </option>
        ))}
      </select>
    </label>
  );
}
