// web/src/lib/lang.test.js
import { describe, expect, it } from "vitest";
import { LANG_KEY, browserLang, format, savedChoice, translate } from "./lang.js";

const codes = ["en", "de", "es", "fr", "it", "pt"];

describe("browserLang", () => {
  it("takes the first browser language that has a catalog", () => {
    expect(browserLang(["nl-NL", "fr-FR", "en"], codes)).toBe("fr");
  });
  it("falls back to English", () => {
    expect(browserLang(["ja-JP"], codes)).toBe("en");
    expect(browserLang([], codes)).toBe("en");
  });
});

describe("savedChoice", () => {
  const store = (v) => ({ getItem: (k) => (k === LANG_KEY ? v : null) });
  it("returns a saved language", () => expect(savedChoice(store("de"), codes)).toBe("de"));
  it("is auto when nothing or something unknown is saved", () => {
    expect(savedChoice(store(null), codes)).toBe("auto");
    expect(savedChoice(store("xx"), codes)).toBe("auto");
  });
  it("is auto when the storage throws or is missing", () => {
    expect(savedChoice({ getItem: () => { throw new Error("blocked"); } }, codes)).toBe("auto");
    expect(savedChoice(null, codes)).toBe("auto");
  });
});

describe("translate and format", () => {
  const catalogs = { fr: { Check: "Vérifier", "✓ Frigate %s": "✓ Frigate %s" } };
  it("translates, and keeps the English text when missing", () => {
    expect(translate(catalogs, "fr", "Check")).toBe("Vérifier");
    expect(translate(catalogs, "fr", "Missing")).toBe("Missing");
    expect(translate(catalogs, "en", "Check")).toBe("Check");
  });
  it("fills %s and %d in order", () => {
    expect(format("%s has %d cameras", ["Garage", 3])).toBe("Garage has 3 cameras");
  });
});
