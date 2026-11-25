import { useEffect, useState, type ReactNode } from "react";
import { Navigate } from "react-router-dom";

import { refreshSession } from "../../lib/api";
import { authApi } from "./api";
import { useAuthStore } from "./store";

/** Blocks its children until a session is present, bootstrapping via refresh. */
export function RequireAuth({ children }: { children: ReactNode }) {
  const accessToken = useAuthStore((s) => s.accessToken);
  const setUser = useAuthStore((s) => s.setUser);
  const clear = useAuthStore((s) => s.clear);
  const [status, setStatus] = useState<"loading" | "ready" | "denied">(
    accessToken ? "ready" : "loading",
  );

  useEffect(() => {
    if (accessToken) return;
    let alive = true;
    void (async () => {
      const refreshed = await refreshSession();
      if (!alive) return;
      if (!refreshed) {
        setStatus("denied");
        return;
      }
      try {
        setUser(await authApi.me());
        setStatus("ready");
      } catch {
        clear();
        setStatus("denied");
      }
    })();
    return () => {
      alive = false;
    };
  }, [accessToken, setUser, clear]);

  if (status === "loading") {
    return <p className="p-8 text-center text-sm text-muted-foreground">Memuat…</p>;
  }
  if (status === "denied") {
    return <Navigate to="/login" replace />;
  }
  return <>{children}</>;
}