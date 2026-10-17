import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SAVE_DELAY, app, change, flush, load, sendTest } from "./store.svelte.js";

// fakeServer answers like the Go handler: settings, saves (recorded), test.
function fakeServer({ refuse = false } = {}) {
  const puts = [];
  const tests = [];
  globalThis.fetch = vi.fn(async (path, opts = {}) => {
    const json = (b, status = 200) => new Response(JSON.stringify(b), { status });
    if (path === "api/settings" && opts.method === "PUT") {
      puts.push(JSON.parse(opts.body));
      return refuse ? json({ error: "invalid cooldown" }, 400) : json({ status: "ok" });
    }
    if (path === "api/settings") {
      return json({ mode: "reviews", timezone: "UTC", cameras: [{ name: "garage" }, { name: "entree" }], chats: ["moi"],
        overlay: { notify: { snapshot: true, clip: true, gif: false, group: "0s" }, cameras: {} }, custom: false });
    }
    if (path === "api/test") { tests.push({ body: JSON.parse(opts.body), putsSoFar: puts.length }); return json({ status: "ok" }); }
    return json({});
  });
  return { puts, tests };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("store", () => {
  it("loads the settings, with an empty entry per camera", async () => {
    fakeServer();
    await load();
    expect(app.overlay.cameras).toEqual({ garage: {}, entree: {} });
    expect(app.data.mode).toBe("reviews");
  });

  it("saves once, shortly after a burst of changes", async () => {
    const { puts } = fakeServer();
    await load();
    change({ gif: true, clip: false });
    change({ crop: true });
    expect(puts).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(SAVE_DELAY + 10);
    expect(puts).toHaveLength(1);
    expect(puts[0].notify).toMatchObject({ gif: true, clip: false, crop: true });
    expect(puts[0].cameras).toEqual({});
    expect(app.status.kind).toBe("saved");
  });

  it("shows a refused save", async () => {
    fakeServer({ refuse: true });
    await load();
    change({ crop: true });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY + 10);
    expect(app.status.kind).toBe("dirty");
    expect(app.error).toContain("invalid cooldown");
  });

  it("a test goes out with the pending change saved", async () => {
    const { tests } = fakeServer();
    await load();
    change({ snapshot: false, clip: false, gif: false });
    await sendTest("garage");
    expect(tests).toEqual([{ body: { camera: "garage" }, putsSoFar: 1 }]);
  });

  it("flush with nothing pending sends nothing", async () => {
    const { puts } = fakeServer();
    await load();
    expect(await flush()).toBe(true);
    expect(puts).toHaveLength(0);
  });
});
