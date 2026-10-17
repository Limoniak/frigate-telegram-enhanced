import { describe, expect, it } from "vitest";
import { KEPT, inHour, tooOld } from "./activity.js";

const hour = Date.parse("2026-10-17T19:00:00Z") / 1000;

describe("activity", () => {
  it("inHour: no window keeps everything", () => expect(inHour({ at: "2020-01-01T00:00:00Z" }, null)).toBe(true));
  it("inHour: a sent entry counts at its send time, not at the event's start", () => {
    const e = { sent: true, at: "2026-10-17T18:58:00Z", sent_at: "2026-10-17T19:05:00Z" };
    expect(inHour(e, hour)).toBe(true);
    expect(inHour({ ...e, at: "2026-10-17T19:30:00Z", sent_at: "2026-10-17T20:05:00Z" }, hour)).toBe(false);
  });
  it("inHour: without sent_at (older file) or when ignored, the start counts", () => {
    expect(inHour({ sent: true, at: "2026-10-17T19:05:00Z" }, hour)).toBe(true);
    expect(inHour({ sent: false, at: "2026-10-17T19:05:00Z", sent_at: "2026-10-17T22:00:00Z" }, hour)).toBe(true);
  });
  it("inHour: the window is (hour, hour + 1 h]", () => {
    expect(inHour({ at: "2026-10-17T19:00:00Z" }, hour)).toBe(false);
    expect(inHour({ at: "2026-10-17T20:00:00Z" }, hour)).toBe(true);
  });
  it("tooOld: only a full history and an hour before its oldest entry", () => {
    const full = Array.from({ length: KEPT }, (_, i) => ({ at: new Date(Date.parse("2026-10-17T21:00:00Z") - i * 60000).toISOString() }));
    expect(tooOld(full, hour)).toBe(true);
    expect(tooOld(full, hour + 3 * 3600)).toBe(false);
    expect(tooOld(full.slice(1), hour)).toBe(false);
    expect(tooOld(full, null)).toBe(false);
  });
});
