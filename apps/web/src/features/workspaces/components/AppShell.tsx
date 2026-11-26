import { useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useMatch, useNavigate } from "react-router-dom";

import { Button } from "../../../components/ui";
import { authApi } from "../../auth/api";
import { useAuthStore } from "../../auth/store";
import { WorkspaceSelector } from "./WorkspaceSelector";

/** Authenticated layout: app header (workspace switcher, identity, logout). */
export function AppShell() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const user = useAuthStore((s) => s.user);
  const clear = useAuthStore((s) => s.clear);
  const match = useMatch("/app/:workspaceId/*");
  const activeId = match?.params.workspaceId ?? null;

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
    <div className="min-h-screen bg-background">
      <header className="border-b border-border bg-surface-raised">
        <div className="mx-auto flex w-full max-w-5xl flex-wrap items-center gap-4 p-4">
          <Link to="/app" className="text-lg font-semibold text-foreground">
            InsightFlow AI
          </Link>
          <div className="min-w-48 flex-1">
            <WorkspaceSelector
              label={null}
              value={activeId}
              onChange={(ws) => navigate(`/app/${ws.id}/overview`)}
            />
          </div>
          {activeId && (
            <nav className="flex items-center gap-4 text-sm font-medium">
              <Link to={`/app/${activeId}/overview`} className="text-muted-foreground hover:text-foreground">
                Ikhtisar
              </Link>
              <Link to={`/app/${activeId}/datasets`} className="text-muted-foreground hover:text-foreground">
                Dataset
              </Link>
            </nav>
          )}
          <span className="text-sm text-muted-foreground">{user?.email}</span>
          <Button variant="ghost" onClick={onLogout}>
            Keluar
          </Button>
        </div>
      </header>
      <main className="mx-auto w-full max-w-5xl p-6">
        <Outlet />
      </main>
    </div>
  );
}
