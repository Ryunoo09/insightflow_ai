import { useEffect, useState, type FormEvent } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router-dom";

import { Button, Card, FormError } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { authApi, type OtpChallenge } from "../api";
import { useAuthStore } from "../store";
import { OtpInput } from "../components/OtpInput";

export function VerifyOtpPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const setSession = useAuthStore((s) => s.setSession);

  const [challenge] = useState<OtpChallenge | null>(
    (location.state as { challenge?: OtpChallenge } | null)?.challenge ?? null,
  );
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [resent, setResent] = useState(false);

  useEffect(() => {
    if (!challenge) return;
    setCooldown(challenge.resend_after);
  }, [challenge]);

  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setTimeout(() => setCooldown((s) => s - 1), 1000);
    return () => clearTimeout(timer);
  }, [cooldown]);

  if (!challenge) {
    return <Navigate to="/login" replace />;
  }
  const ch = challenge;

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError(null);
    try {
      const session = await authApi.verifyOtp(ch.challenge_id, code);
      setSession(session.access_token, session.user);
      navigate("/app", { replace: true });
    } catch (err) {
      setCode("");
      setError(err instanceof ApiError ? err.message : "Verifikasi gagal. Coba lagi.");
    } finally {
      setPending(false);
    }
  }

  async function onResend() {
    if (!challenge || cooldown > 0) return;
    setError(null);
    setResent(false);
    try {
      const res = await authApi.resendOtp(challenge.challenge_id);
      setCooldown(res.resend_after);
      setResent(true);
      setCode("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Gagal mengirim ulang kode.");
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-sm">
        <h1 className="text-2xl font-semibold">Verifikasi email</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Kode 6 digit dikirim ke <span className="font-medium text-foreground">{challenge.email_hint}</span>.
        </p>

        <form className="mt-6 space-y-4" onSubmit={onSubmit}>
          <OtpInput value={code} onChange={setCode} disabled={pending} describedBy={error ? "otp-error" : undefined} />

          {error && <FormError id="otp-error">{error}</FormError>}
          {resent && <p className="text-sm text-success">Kode baru sudah dikirim.</p>}

          <Button type="submit" className="w-full" disabled={pending || code.length !== 6}>
            {pending ? "Memverifikasi..." : "Verifikasi"}
          </Button>
        </form>

        <div className="mt-4 flex items-center justify-between text-sm">
          <button
            type="button"
            onClick={onResend}
            disabled={cooldown > 0}
            className="font-medium text-primary hover:underline disabled:cursor-not-allowed disabled:text-muted-foreground disabled:no-underline"
          >
            {cooldown > 0 ? `Kirim ulang dalam ${cooldown}s` : "Kirim ulang kode"}
          </button>
          <Link to="/login" className="text-muted-foreground hover:underline">
            Ganti akun
          </Link>
        </div>
      </Card>
    </main>
  );
}
