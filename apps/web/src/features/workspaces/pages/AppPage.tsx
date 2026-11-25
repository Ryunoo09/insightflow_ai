import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";

import { Badge, Button, Card, FieldLabel, FormError, Input } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { authApi } from "../../auth/api";
import { useAuthStore } from "../../auth/store";
import { workspacesApi, type Workspace } from "../api";
import { WorkspaceSelector } from "../components/WorkspaceSelector";

export function AppPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const user = useAuthStore((s) => s.user);
  const clear = useAuthStore((s) => s.clear);

  const [active, setActive] = useState<Workspace | null>(null);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const { data: detail } = useQuery({
    queryKey: ["workspace", active?.id],
    queryFn: () => workspacesApi.get(active!.id),
    enabled: active !== null,
  });

  const createMutation = useMutation({
    mutationFn: workspacesApi.create,
    onSuccess: (ws) => {
      setName("");
      setActive(ws);
      void queryClient.invalidateQueries({ queryKey: ["workspaces"] });
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

  async function onLogout() {
    try {
      await authApi.logout(false);
    } finally {
      clear();
      queryClient.clear();
      navigate("/login", { replace: true });
    }
  }

  return (
    <main className="mx-auto flex min-h-screen w-full max-w-3xl flex-col gap-6 p-6">
      <header className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Ruang kerja</h1>
          <p className="text-sm text-muted-foreground">{user?.email}</p>
        </div>
        <Button variant="ghost" onClick={onLogout}>
          Keluar
        </Button>
      </header>

      <Card className="space-y-4">
        <WorkspaceSelector value={active?.id ?? null} onChange={setActive} />
        {detail && (
          <div className="flex items-center gap-3 text-sm">
            <span className="font-medium text-foreground">{detail.name}</span>
            <Badge>{detail.role}</Badge>
            <span className="text-muted-foreground">/{detail.slug}</span>
          </div>
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
    </main>
  );
}
