import { describe, expect, it } from "vitest";

import { STRENGTH_LABELS, scorePassword } from "./passwordStrength";

describe("scorePassword", () => {
  it("rates empty and short passwords weakest", () => {
    expect(scorePassword("")).toBe(0);
    expect(scorePassword("short")).toBe(0);
  });

  it("rates a bare 10-character lowercase password weak", () => {
    expect(scorePassword("abcdefghij")).toBe(1);
  });

  it("caps a long mixed password at the strongest bucket", () => {
    expect(scorePassword("Abcdefghijkl1!")).toBe(4);
  });

  it("stays within 0..4 and labels every bucket", () => {
    for (const pwd of ["", "a", "abcdefghij", "abcdefghijklm", "Ab1!", "Abcdefghijklmnop1!"]) {
      const score = scorePassword(pwd);
      expect(score).toBeGreaterThanOrEqual(0);
      expect(score).toBeLessThanOrEqual(4);
      expect(STRENGTH_LABELS[score]).toBeTruthy();
    }
  });
});
