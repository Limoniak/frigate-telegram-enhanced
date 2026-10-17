import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SAVE_DELAY, app, change, flush, load, pause, sendTest } from "./store.svelte.js";

// fakeServer answers like the Go handler: settings, saves (recorded), test.
function fakeServer({ refuse = false } = {}) {
  const server = { down: false, hold: null, refuse };
  const puts = [];
  const tests = [];
  globalThis.fetch = vi.fn(async (path, opts = {}) => {
    if (server.down && path !== "api/settings") throw new TypeError("network down");
    if (server.down && opts.method === "PUT") throw new TypeError("network down");
    const json = (b, status = 200) => new Response(JSON.stringify(b), { status });
    if (path === "api/settings" && opts.method === "PUT") {
      puts.push(JSON.parse(opts.body));
      if (server.hold) await server.hold;
      return server.refuse ? json({ error: "invalid cooldown" }, 400) : json({ status: "ok" });
    }
    if (path === "api/settings") {
      return json({ mode: "reviews", timezone: "UTC", cameras: [{ name: "garage" }, { name: "entree" }], chats: ["moi"],
        overlay: { notify: { snapshot: true, clip: true, gif: false, group: "0s" }, cameras: {} }, custom: false });
    }
    if (path === "api/test") { tests.push({ body: JSON.parse(opts.body), putsSoFar: puts.length }); return json({ status: "ok" }); }
    return json({});
  });
  return { puts, tests, server };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  vi.clearAllTimers();
  vi.useRealTimers();
});

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

  it("a network failure leaves the next saves working", async () => {
    const { puts, server } = fakeServer();
    await load();
    server.down = true;
    change({ crop: true });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY + 10);
    expect(app.status.kind).toBe("dirty");
    expect(app.error).toContain("network down");
    server.down = false;
    change({ gif: true });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY + 10);
    expect(puts).toHaveLength(1);
    expect(puts[0].notify).toMatchObject({ crop: true, gif: true });
    expect(app.status.kind).toBe("saved");
    expect(app.error).toBe("");
  });

  it("a change made during a save is saved right after", async () => {
    const { puts, server } = fakeServer();
    await load();
    let release;
    server.hold = new Promise((r) => { release = r; });
    change({ crop: true });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY + 10);
    expect(puts).toHaveLength(1);
    change({ gif: true });
    server.hold = null;
    release();
    await vi.advanceTimersByTimeAsync(SAVE_DELAY + 10);
    expect(puts).toHaveLength(2);
    expect(puts[1].notify.gif).toBe(true);
    expect(app.status.kind).toBe("saved");
  });

  it("a test is not sent when the save is refused", async () => {
    const { tests } = fakeServer({ refuse: true });
    await load();
    change({ crop: true });
    await expect(sendTest("garage")).rejects.toThrow("the settings could not be saved");
    expect(tests).toHaveLength(0);
  });

  it("a pause that cannot reach the server shows the failure", async () => {
    const { server } = fakeServer();
    await load();
    server.down = true;
    globalThis.fetch.mockImplementation(async () => { throw new TypeError("network down"); });
    await pause(30);
    expect(app.error.startsWith("Action failed:")).toBe(true);
  });

  it("flush saves at once, without waiting for the timer", async () => {
    const { puts } = fakeServer();
    await load();
    change({ crop: true });
    expect(await flush()).toBe(true);
    expect(puts).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(SAVE_DELAY + 10);
    expect(puts).toHaveLength(1);
  });
});
