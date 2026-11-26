/** Password strength bucket: 0 (weakest) to 4 (strongest). */
export type StrengthScore = 0 | 1 | 2 | 3 | 4;

export const STRENGTH_LABELS: Record<StrengthScore, string> = {
  0: "Sangat lemah",
  1: "Lemah",
  2: "Sedang",
  3: "Kuat",
  4: "Sangat kuat",
};

/**
 * Heuristic hint mirrored by the meter. It is presentation only: the backend
 * stays the authority on whether a password is accepted (min 10 characters).
 */
export function scorePassword(password: string): StrengthScore {
  let raw = 0;
  if (password.length >= 10) raw++;
  if (password.length >= 14) raw++;
  if (/[a-z]/.test(password) && /[A-Z]/.test(password)) raw++;
  if (/\d/.test(password)) raw++;
  if (/[^A-Za-z0-9]/.test(password)) raw++;
  return Math.min(raw, 4) as StrengthScore;
}
