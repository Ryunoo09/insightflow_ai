import { cn } from "../../../lib/cn";
import { STRENGTH_LABELS, scorePassword } from "../passwordStrength";

const SEGMENT_COLORS = ["bg-danger", "bg-danger", "bg-warning", "bg-success", "bg-success"] as const;

/** Four-segment strength meter shown while typing a new password. */
export function PasswordStrength({ password }: { password: string }) {
  if (password === "") return null;
  const score = scorePassword(password);

  return (
    <div className="mt-2" aria-live="polite">
      <div className="flex gap-1" aria-hidden="true">
        {[0, 1, 2, 3].map((segment) => (
          <span
            key={segment}
            className={cn("h-1 flex-1 rounded-full", segment <= score ? SEGMENT_COLORS[score] : "bg-border")}
          />
        ))}
      </div>
      <p className="mt-1 text-xs text-muted-foreground">Kekuatan password: {STRENGTH_LABELS[score]}</p>
    </div>
  );
}
