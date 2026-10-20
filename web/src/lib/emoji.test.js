import { describe, expect, it } from "vitest";
import { loadEmojis, sameEmoji, searchEmojis } from "./emoji.js";

describe("emoji", () => {
  it("finds an emoji by its name or a keyword, in the page's language, accents aside", async () => {
    const fr = await loadEmojis("fr");
    expect(searchEmojis(fr, "porte").map((e) => e.unicode)).toContain("🚪");
    expect(searchEmojis(fr, "VOITURE").map((e) => e.unicode)).toContain("🚗");
    expect(searchEmojis(fr, "eglise").some((e) => sameEmoji(e.unicode, "⛪"))).toBe(true); // église
    expect(searchEmojis(fr, "")).toHaveLength(fr.length);
  });
  it("leaves out skin tones, and falls back to English for a language it lacks", async () => {
    const sl = await loadEmojis("sl");
    expect(sl.find((e) => e.unicode === "🚪").label).toBe("door");
    expect(sl.some((e) => e.label.includes("skin tone") && [...e.unicode].length === 1)).toBe(false);
  });
  it("an emoji is the same with or without its variation selector", () => {
    expect(sameEmoji("🏚️", "🏚")).toBe(true);
    expect(sameEmoji("🚪", "🏠")).toBe(false);
  });
});
