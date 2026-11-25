import { API_BASE, api } from "../../lib/api";
import type { AuthUser } from "./store";

export type OtpChallenge = {
  status: "otp_required";
  challenge_id: string;
  expires_in: number;
  resend_after: number;
  email_hint: string;
};

export type SessionResponse = {
  user: AuthUser;
  access_token: string;
  expires_in: number;
};

export type ResendResponse = {
  expires_in: number;
  resend_after: number;
  email_hint: string;
};

export const authApi = {
  register: (body: { email: string; password: string; full_name: string }) =>
    api<OtpChallenge>("/auth/register", { method: "POST", body, auth: false }),

  login: (body: { email: string; password: string }) =>
    api<OtpChallenge>("/auth/login", { method: "POST", body, auth: false }),

  verifyOtp: (challengeId: string, code: string) =>
    api<SessionResponse>("/auth/otp/verify", {
      method: "POST",
      body: { challenge_id: challengeId, code },
      auth: false,
    }),

  resendOtp: (challengeId: string) =>
    api<ResendResponse>("/auth/otp/resend", {
      method: "POST",
      body: { challenge_id: challengeId },
      auth: false,
    }),

  me: () => api<AuthUser>("/users/me"),

  logout: (allDevices: boolean) =>
    api<void>(`/auth/logout${allDevices ? "?all_devices=true" : ""}`, {
      method: "POST",
      retry: false,
    }),

  googleStartUrl: () => `${API_BASE}/auth/google/start`,
};