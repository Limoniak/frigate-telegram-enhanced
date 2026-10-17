import { describe, expect, it } from "vitest";
import { STYLES, apply, cameraOverrides, effective, matches, same, seconds, styleOf, toSend } from "./settings.js";

const overlay = () => ({
  notify: { snapshot: true, clip: true, gif: false, crop: false, cooldown: "1m0s", group: "0s", labels: ["person", "car"] },
  cameras: { garage: {}, entree: { clip: false } },
  recipients: {}, external_url: "", home_assistant_url: "",
});

describe("seconds and same", () => {
  it("reads Go durations", () => {
    expect(seconds("1m0s")).toBe(60);
    expect(seconds("1h30m")).toBe(5400);
    expect(seconds("")).toBe(0);
  });
  it("compares by kind: durations, lists in any order, booleans", () => {
    expect(same("cooldown", "1m", "1m0s")).toBe(true);
    expect(same("labels", ["car", "person"], ["person", "car"])).toBe(true);
    expect(same("crop", undefined, false)).toBe(true);
  });
});

describe("effective, matches, apply", () => {
  it("a camera inherits what it does not set", () => {
    expect(effective(overlay(), "entree").clip).toBe(false);
    expect(effective(overlay(), "garage").clip).toBe(true);
  });
  it("sets a global choice", () => {
    const o = overlay();
    apply(o, { gif: true, clip: false });
    expect(matches({ gif: true, clip: false }, o.notify)).toBe(true);
  });
  it("a camera choice equal to the global one removes the override", () => {
    const o = overlay();
    apply(o, { clip: true }, "entree");
    expect(o.cameras.entree).toEqual({});
    apply(o, { clip: false }, "garage");
    expect(o.cameras.garage).toEqual({ clip: false });
  });
});

describe("toSend", () => {
  it("drops cameras without overrides", () => {
    expect(Object.keys(toSend(overlay()).cameras)).toEqual(["entree"]);
  });
});

describe("styleOf and cameraOverrides", () => {
  it("names the style the settings match", () => {
    expect(styleOf({ snapshot: true, clip: true, gif: false })).toBe("clip");
    expect(styleOf({ snapshot: true, clip: false, gif: true })).toBe("gif");
    expect(styleOf({ snapshot: false, clip: true, gif: false })).toBe("custom");
    expect(STYLES.map((s) => s.id)).toEqual(["clip", "photo", "gif", "text"]);
  });
  it("lists the cameras that set one of the keys themselves", () => {
    expect(cameraOverrides(overlay(), ["snapshot", "clip", "gif"])).toEqual(["entree"]);
    expect(cameraOverrides(overlay(), ["crop"])).toEqual([]);
  });
});
