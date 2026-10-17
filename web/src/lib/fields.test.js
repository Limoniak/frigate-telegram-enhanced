import { describe, expect, it } from "vitest";
import { GROUPS, cameraSummary, clampScore, defaultFor, fieldsFor, sensitivityEffect, untracked, validDuration, validURL } from "./fields.js";

const T = (s) => s;
const Tf = (s, ...a) => { let i = 0; return s.replace(/%d/g, () => a[i++]); };
const keys = (fs) => fs.map((f) => f.key);

describe("fieldsFor", () => {
  it("filters by mode and presence for the global level", () => {
    const k = keys(fieldsFor({ mode: "events", presence: false }, null, T));
    expect(k).not.toContain("severity");
    expect(k).not.toContain("when_home");
    expect(k).toContain("min_score");
    expect(keys(fieldsFor({ mode: "reviews", presence: true }, null, T))).toEqual(expect.arrayContaining(["severity", "when_home"]));
  });
  it("leaves out the global-only fields and the switch for a camera", () => {
    const k = keys(fieldsFor({ mode: "events", presence: true }, "garage", T));
    for (const x of ["enabled", "group", "compress_clips"]) expect(k).not.toContain(x);
    expect(k).toContain("cooldown");
  });
  it("describes the Links group", () => {
    expect(GROUPS(T).find((g) => g.links).globalOnly).toBe(true);
  });
  it("defaults to the inherited value, else an empty one", () => {
    expect(defaultFor({ type: "chips" }, ["a"])).toEqual(["a"]);
    expect(defaultFor({ type: "duration" }, null)).toBe("0s");
  });
});

describe("validation", () => {
  it("checks durations", () => {
    expect(validDuration("90s")).toBe(true);
    expect(validDuration(" 1h30m ")).toBe(true);
    expect(validDuration("5 minutes")).toBe(false);
    expect(validDuration("")).toBe(false);
  });
  it("checks addresses", () => {
    expect(validURL("")).toBe(true);
    expect(validURL("https://frigate.example.com")).toBe(true);
    expect(validURL("frigate.local")).toBe(false);
  });
  it("clamps scores", () => {
    expect(clampScore("1.7")).toBe(1);
    expect(clampScore("abc")).toBe(0);
    expect(clampScore("0.456")).toBe(0.46);
  });
});

describe("untracked", () => {
  const overlay = { notify: { labels: ["person", "car"] }, cameras: { garage: {} } };
  it("lists the objects Frigate does not track", () => {
    expect(untracked({ cameras: [{ name: "garage", labels: ["person"] }] }, overlay, "garage")).toEqual(["car"]);
  });
  it("says nothing without Frigate's list", () => {
    expect(untracked({ cameras: [{ name: "garage" }] }, overlay, "garage")).toEqual([]);
  });
});

describe("sensitivityEffect", () => {
  it("waits for detections", () => {
    expect(sensitivityEffect([], 0.5, T, Tf)).toMatch(/^Recent activity will show/);
  });
  it("counts the ignored detections", () => {
    expect(sensitivityEffect([{ score: 0.4, sent: true }, { score: 0.8 }], 0.5, T, Tf))
      .toBe("Out of the last 2 detections, 1 would be ignored for a low score, including 1 that were sent.");
  });
});

describe("cameraSummary", () => {
  const ctx = { tz: "UTC", lang: "en", T };
  const data = { cameras: [{ name: "garage" }] };
  const overlay = (cam) => ({ notify: { snapshot: true, clip: true, gif: false, labels: ["person"] }, cameras: { garage: cam } });
  it("says a camera is disabled", () => {
    expect(cameraSummary(data, overlay({ enabled: false }), null, "garage", ctx)).toBe("disabled");
  });
  it("summarizes a camera that follows the defaults", () => {
    expect(cameraSummary(data, overlay({}), null, "garage", ctx)).toBe("Photo + video · person · default");
  });
});
