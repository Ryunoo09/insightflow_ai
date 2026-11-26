import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";

import { Badge, Button, Card, FieldLabel, FormError, Input } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { workspacesApi } from "../api";

/** Landing page for /app: pick an existing workspace or create a new one. */
export function AppPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const { data, isPending, isError } = useQuery({
    queryKey: ["workspaces"],
    queryFn: workspacesApi.list,
  });

  const createMutation = useMutation({
    mutationFn: workspacesApi.create,
    onSuccess: (ws) => {
      setName("");
      void queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      navigate(`/app/${ws.id}/overview`);
    },
    onError: (err) => {
      setError(err instanceof ApiError ? err.message : "Gagal membuat ruang kerja.");
    },
  });

  function onCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    createMutation.mutate(name.trim());
  }

  const items = data?.data ?? [];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Pilih ruang kerja</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Ruang kerja memisahkan dataset dan analisis antar tim.
        </p>
      </div>

      <Card className="space-y-3">
        <h2 className="text-lg font-semibold">Ruang kerja kamu</h2>
        {isPending && <p className="text-sm text-muted-foreground">Memuat ruang kerja...</p>}
        {isError && <p className="text-sm text-danger">Gagal memuat ruang kerja.</p>}
        {!isPending && !isError && items.length === 0 && (
          <p className="text-sm text-muted-foreground">Belum ada ruang kerja. Buat yang pertama di bawah.</p>
        )}
        {items.length > 0 && (
          <ul className="divide-y divide-border">
            {items.map((ws) => (
              <li key={ws.id}>
                <button
                  type="button"
                  onClick={() => navigate(`/app/${ws.id}/overview`)}
                  className="flex w-full flex-wrap items-center gap-3 py-3 text-left hover:bg-surface"
                >
                  <span className="font-medium text-foreground">{ws.name}</span>
                  <Badge>{ws.role}</Badge>
                  <span className="text-sm text-muted-foreground">/{ws.slug}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card>
        <h2 className="text-lg font-semibold">Buat ruang kerja baru</h2>
        <form className="mt-4 space-y-4" onSubmit={onCreate}>
          <div>
            <FieldLabel htmlFor="workspace-name">Nama</FieldLabel>
            <Input
              id="workspace-name"
              required
              maxLength={100}
              value={name}
              onChange={(e) => setName(e.target.value)}
              aria-describedby={error ? "workspace-error" : undefined}
            />
          </div>
          {error && <FormError id="workspace-error">{error}</FormError>}
          <Button type="submit" disabled={createMutation.isPending || name.trim() === ""}>
            {createMutation.isPending ? "Membuat..." : "Buat"}
          </Button>
        </form>
      </Card>
    </div>
  );
}
