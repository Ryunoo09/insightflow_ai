import { useRef, type ClipboardEvent, type KeyboardEvent } from "react";

import { cn } from "../../../lib/cn";

const LENGTH = 6;

type Props = {
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  describedBy?: string;
};

/** Six single-digit boxes with auto-advance, paste and arrow-key support. */
export function OtpInput({ value, onChange, disabled, describedBy }: Props) {
  const refs = useRef<Array<HTMLInputElement | null>>([]);
  const digits = Array.from({ length: LENGTH }, (_, i) => value[i] ?? "");

  const setAt = (index: number, char: string) => {
    const next = digits.slice();
    next[index] = char;
    onChange(next.join("").slice(0, LENGTH));
  };

  const focus = (index: number) => refs.current[Math.max(0, Math.min(LENGTH - 1, index))]?.focus();

  const handleChange = (index: number, raw: string) => {
    const clean = raw.replace(/\D/g, "");
    if (clean.length > 1) {
      const next = (value.slice(0, index) + clean).replace(/\D/g, "").slice(0, LENGTH);
      onChange(next);
      focus(next.length);
      return;
    }
    if (clean.length === 0) {
      setAt(index, "");
      return;
    }
    setAt(index, clean);
    focus(index + 1);
  };

  const handleKeyDown = (index: number, event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Backspace" && !digits[index] && index > 0) focus(index - 1);
    if (event.key === "ArrowLeft" && index > 0) focus(index - 1);
    if (event.key === "ArrowRight" && index < LENGTH - 1) focus(index + 1);
  };

  const handlePaste = (event: ClipboardEvent<HTMLInputElement>) => {
    const text = event.clipboardData.getData("text").replace(/\D/g, "").slice(0, LENGTH);
    if (!text) return;
    event.preventDefault();
    onChange(text);
    focus(text.length);
  };

  return (
    <div className="flex gap-2" role="group" aria-label="Kode verifikasi 6 digit">
      {digits.map((digit, index) => (
        <input
          key={index}
          ref={(el) => {
            refs.current[index] = el;
          }}
          value={digit}
          onChange={(e) => handleChange(index, e.target.value)}
          onKeyDown={(e) => handleKeyDown(index, e)}
          onPaste={handlePaste}
          disabled={disabled}
          inputMode="numeric"
          autoComplete="one-time-code"
          maxLength={1}
          aria-label={`Digit ${index + 1}`}
          aria-describedby={describedBy}
          className={cn(
            "h-12 w-10 rounded-md border border-border bg-surface-raised text-center text-lg font-semibold text-foreground",
            "disabled:opacity-60",
          )}
        />
      ))}
    </div>
  );
}