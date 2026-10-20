import { describe, expect, it } from "vitest";
import { labelEmoji, labelText } from "./labels.js";

const T = (s) => s;

describe("labels", () => {
  it("shows a recognized object (Frigate 0.16's -verified) as its object", () => {
    expect(labelText("car-verified", false, T)).toBe(labelText("car", false, T));
    expect(labelEmoji("person-verified", false)).toBe(labelEmoji("person", false));
    expect(labelText("", false, T)).toBe("🔔 Detection");
  });
});
