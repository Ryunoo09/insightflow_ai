import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { Button, Card, FieldLabel, FormError, Input } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { authApi } from "../api";

export function RegisterPage() {
  const navigate = useNavigate();
  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError(null);
    try {
      const challenge = await authApi.register({ email, password, full_name: fullName });
      navigate("/verify-otp", { state: { challenge } });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Gagal mendaftar. Coba lagi.");
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-sm">
        <h1 className="text-2xl font-semibold">Daftar</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Buat akun, lalu verifikasi email dengan kode OTP.
        </p>

        <form className="mt-6 space-y-4" onSubmit={onSubmit}>
          <div>
            <FieldLabel htmlFor="full_name">Nama lengkap</FieldLabel>
            <Input
              id="full_name"
              autoComplete="name"
              required
              maxLength={100}
              value={fullName}
              onChange={(e) => setFullName(e.target.value)}
              aria-describedby={error ? "register-error" : undefined}
            />
          </div>
          <div>
            <FieldLabel htmlFor="email">Email</FieldLabel>
            <Input
              id="email"
              type="email"
              autoComplete="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              aria-describedby={error ? "register-error" : undefined}
            />
          </div>
          <div>
            <FieldLabel htmlFor="password">Password</FieldLabel>
            <Input
              id="password"
              type="password"
              autoComplete="new-password"
              required
              minLength={10}
              maxLength={128}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              aria-describedby={error ? "register-error" : "password-hint"}
            />
            <p id="password-hint" className="mt-1 text-xs text-muted-foreground">
              10–128 karakter; hindari password umum.
            </p>
          </div>

          {error && <FormError id="register-error">{error}</FormError>}

          <Button type="submit" className="w-full" disabled={pending}>
            {pending ? "Memproses…" : "Daftar"}
          </Button>
        </form>

        <p className="mt-6 text-center text-sm text-muted-foreground">
          Sudah punya akun?{" "}
          <Link to="/login" className="font-medium text-primary hover:underline">
            Masuk
          </Link>
        </p>
      </Card>
    </main>
  );
}