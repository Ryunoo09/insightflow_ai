import { useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";

import { Button, Card, FieldLabel, FormError, Input } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { authApi } from "../api";

const OAUTH_ERRORS: Record<string, string> = {
  AUTH_OAUTH_STATE_INVALID: "Permintaan login Google tidak valid. Coba lagi.",
  AUTH_OAUTH_EMAIL_UNVERIFIED: "Email Google belum terverifikasi.",
  AUTH_OAUTH_FAILED: "Login Google gagal. Coba lagi.",
};

export function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const oauthError = new URLSearchParams(location.search).get("error");

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError(null);
    try {
      const challenge = await authApi.login({ email, password });
      navigate("/verify-otp", { state: { challenge } });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Gagal masuk. Coba lagi.");
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-sm">
        <h1 className="text-2xl font-semibold">Masuk</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Masukkan email dan password, lalu kode OTP.
        </p>

        {oauthError && (
          <div className="mt-4">
            <FormError>{OAUTH_ERRORS[oauthError] ?? "Login Google gagal."}</FormError>
          </div>
        )}

        <form className="mt-6 space-y-4" onSubmit={onSubmit}>
          <div>
            <FieldLabel htmlFor="email">Email</FieldLabel>
            <Input
              id="email"
              type="email"
              autoComplete="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              aria-describedby={error ? "login-error" : undefined}
            />
          </div>
          <div>
            <FieldLabel htmlFor="password">Password</FieldLabel>
            <Input
              id="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              aria-describedby={error ? "login-error" : undefined}
            />
          </div>

          {error && <FormError id="login-error">{error}</FormError>}

          <Button type="submit" className="w-full" disabled={pending}>
            {pending ? "Memproses…" : "Masuk"}
          </Button>
        </form>

        <a
          href={authApi.googleStartUrl()}
          className="mt-3 inline-flex min-h-10 w-full items-center justify-center rounded-md border border-border text-sm font-medium hover:bg-surface"
        >
          Masuk dengan Google
        </a>

        <p className="mt-6 text-center text-sm text-muted-foreground">
          Belum punya akun?{" "}
          <Link to="/register" className="font-medium text-primary hover:underline">
            Daftar
          </Link>
        </p>
      </Card>
    </main>
  );
}