import { describe, expect, it, vi } from "vitest";
import { chartTicks, locale, messageTime, timeText, untilText, hourStart, whenText } from "./time.js";

// A British browser, whatever the machine running the tests.
vi.stubGlobal("navigator", { languages: ["en-GB"] });

const T = (s) => s;
const now = new Date("2026-10-17T19:30:00Z"); // 21:30 in Paris

describe("time", () => {
  it("writes a time as the Telegram messages do, in their layout and on a 24-hour clock", () => {
    expect(messageTime("2026-10-17T19:47:05Z", { tz: "Europe/Paris", T })).toBe("Oct 17 21:47:05");
    const fr = (s) => ({ "Jan 2 15:04:05": "02/01 15:04:05" })[s] ?? s;
    expect(messageTime("2026-03-04T23:05:09Z", { tz: "Europe/Paris", T: fr })).toBe("05/03 00:05:09");
  });
  it("English follows the browser's variant, British by default", () => {
    expect(locale("en", [])).toBe("en-GB");
    expect(locale("en", ["fr-FR", "en-US"])).toBe("en-US");
    expect(locale("fr", ["en-US"])).toBe("fr");
    expect(timeText("2026-10-17T19:47:05Z", { tz: "Europe/Paris", lang: "en" })).toBe("21:47"); // the stubbed browser is British
  });
  it("chartTicks: the bars of the clock's 00, 06, 12 and 18 h in the zone", () => {
    const paris = Date.parse("2026-10-17T19:00:00Z") / 1000; // 21:00 → bars from 22:00 the day before
    const ticks = chartTicks(paris, "Europe/Paris");
    expect(ticks.map((k) => timeText(k.t * 1000, { tz: "Europe/Paris", lang: "fr" }))).toEqual(["00:00", "06:00", "12:00", "18:00"]);
    expect(ticks[0].x).toBe(2 / 24);
    const kolkata = Date.parse("2026-10-17T19:30:00Z") / 1000; // 01:00 there
    expect(chartTicks(kolkata, "Asia/Kolkata").map((k) => timeText(k.t * 1000, { tz: "Asia/Kolkata", lang: "fr" }))).toEqual(["06:00", "12:00", "18:00", "00:00"]);
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
  it("hourStart: the last bar is the current hour, each bar one clock hour", () => {
    expect(hourStart(1760900400, 23)).toBe(1760900400);
    expect(hourStart(1760900400, 0)).toBe(1760900400 - 23 * 3600);
  });
});
