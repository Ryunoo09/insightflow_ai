import { useEffect, useState, type ReactNode } from "react";
import { Navigate } from "react-router-dom";

import { refreshSession } from "../../lib/api";
import { authApi } from "./api";
import { useAuthStore } from "./store";

/** Blocks its children until a session is present, bootstrapping via refresh. */
export function RequireAuth({ children }: { children: ReactNode }) {
  const setUser = useAuthStore((s) => s.setUser);
  const clear = useAuthStore((s) => s.clear);
  const [status, setStatus] = useState<"loading" | "ready" | "denied">(() =>
    useAuthStore.getState().accessToken ? "ready" : "loading",
  );

  // Depends only on the stable store actions: refreshSession() sets the access
  // token, so keying the effect on it would cancel the in-flight bootstrap and
  // leave the screen stuck on "loading".
  useEffect(() => {
    if (useAuthStore.getState().accessToken) {
      setStatus("ready");
      return;
    }
    let cancelled = false;
    void (async () => {
      const refreshed = await refreshSession();
      if (cancelled) return;
      if (!refreshed) {
        setStatus("denied");
        return;
      }
      try {
        setUser(await authApi.me());
        if (!cancelled) setStatus("ready");
      } catch {
        clear();
        if (!cancelled) setStatus("denied");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [setUser, clear]);

  if (status === "loading") {
    return <p className="p-8 text-center text-sm text-muted-foreground">Memuat…</p>;
  }
  if (status === "denied") {
    return <Navigate to="/login" replace />;
  }
  return <>{children}</>;
}