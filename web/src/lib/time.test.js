import { describe, expect, it } from "vitest";
import { chartTicks, locale, timeText, untilText, hourStart, whenText } from "./time.js";

const T = (s) => s;
const now = new Date("2026-10-17T19:30:00Z"); // 21:30 in Paris

describe("time", () => {
  it("English uses the 24-hour British locale", () => expect(locale("en")).toBe("en-GB"));
  it("chartTicks: the clock's 00, 06, 12 and 18 h in the zone, placed in the 24 h", () => {
    const ticks = chartTicks(now.getTime(), "Europe/Paris"); // 21:30 → from 21:30 the day before
    expect(ticks.map((k) => timeText(k.t * 1000, { tz: "Europe/Paris", lang: "fr" }))).toEqual(["00:00", "06:00", "12:00", "18:00"]);
    expect(ticks[0].x).toBeCloseTo(2.5 / 24);
    expect(chartTicks(now.getTime(), "Asia/Kolkata").map((k) => timeText(k.t * 1000, { tz: "Asia/Kolkata", lang: "fr" }))).toEqual(["06:00", "12:00", "18:00", "00:00"]);
  });
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
  it("hourStart: bar i covers (now-(24-i)h, now-(23-i)h], the last bar the last hour", () => {
    const at = 1760900400500;
    expect(hourStart(at, 23)).toBe(1760900400 - 3600);
    expect(hourStart(at, 0)).toBe(1760900400 - 24 * 3600);
  });
});
