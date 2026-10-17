// web/src/lib/theme.test.js
import { afterEach, describe, expect, it, vi } from "vitest";
import { THEME_KEY, applyTheme, savedTheme } from "./theme.js";

function fakeBrowser(saved) {
  const store = new Map(saved ? [[THEME_KEY, saved]] : []);
  const attrs = new Map();
  vi.stubGlobal("localStorage", { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v), removeItem: (k) => store.delete(k) });
  vi.stubGlobal("document", { documentElement: { setAttribute: (k, v) => attrs.set(k, v), removeAttribute: (k) => attrs.delete(k) } });
  return { store, attrs };
}

afterEach(() => vi.unstubAllGlobals());

describe("savedTheme", () => {
  it("returns a saved choice", () => {
    fakeBrowser("dark");
    expect(savedTheme()).toBe("dark");
  });
  it("is auto when nothing or something unknown is saved", () => {
    fakeBrowser(null);
    expect(savedTheme()).toBe("auto");
    fakeBrowser("blue");
    expect(savedTheme()).toBe("auto");
  });
  it("is auto when the storage throws", () => {
    vi.stubGlobal("localStorage", { getItem: () => { throw new Error("blocked"); } });
    expect(savedTheme()).toBe("auto");
  });
});

describe("applyTheme", () => {
  it("sets the attribute and saves a forced theme", () => {
    const { store, attrs } = fakeBrowser(null);
    applyTheme("light");
    expect(attrs.get("data-theme")).toBe("light");
    expect(store.get(THEME_KEY)).toBe("light");
  });
  it("removes both on auto", () => {
    const { store, attrs } = fakeBrowser("dark");
    attrs.set("data-theme", "dark");
    applyTheme("auto");
    expect(attrs.has("data-theme")).toBe(false);
    expect(store.has(THEME_KEY)).toBe(false);
  });
  it("still sets the attribute when the storage throws", () => {
    const { attrs } = fakeBrowser(null);
    vi.stubGlobal("localStorage", { setItem: () => { throw new Error("blocked"); } });
    applyTheme("dark");
    expect(attrs.get("data-theme")).toBe("dark");
  });
});
