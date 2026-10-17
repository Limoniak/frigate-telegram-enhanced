import { describe, expect, it } from "vitest";
import { locale, timeText, untilText, whenText } from "./time.js";

const T = (s) => s;
const now = new Date("2026-10-17T19:30:00Z"); // 21:30 in Paris

describe("time", () => {
  it("English uses the 24-hour British locale", () => expect(locale("en")).toBe("en-GB"));
  it("timeText shows the time in the service's time zone", () => {
    expect(timeText("2026-10-17T19:47:05Z", { tz: "Europe/Paris", lang: "fr" })).toBe("21:47");
  });
  it("untilText: forever, today, another day", () => {
    const ctx = { tz: "Europe/Paris", lang: "en", T, now };
    expect(untilText(null, ctx)).toBe("until resumed");
    expect(untilText("2026-10-17T20:30:00Z", ctx)).toBe("until 22:30");
    expect(untilText("2026-10-18T06:00:00Z", ctx)).toMatch(/^until .*Sunday.*08:00/);
  });
  it("whenText: today, yesterday", () => {
    const ctx = { tz: "Europe/Paris", lang: "en", T, now };
    expect(whenText("2026-10-17T19:00:00Z", ctx)).toBe("21:00");
    expect(whenText("2026-10-16T19:00:00Z", ctx)).toBe("yesterday 21:00");
  });
});
