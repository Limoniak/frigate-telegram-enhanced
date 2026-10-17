import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SAVE_DELAY, app, change, edit, flush, leave, load, pause, resetSettings, sendTest } from "./store.svelte.js";

// fakeServer answers like the Go handler: settings, saves (recorded), test.
function fakeServer({ refuse = false, refuseReset = false } = {}) {
  const server = { down: false, hold: null, holdReset: null, refuse };
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
    if (path === "api/settings/reset" && server.holdReset) await server.holdReset;
    if (path === "api/settings/reset") return refuseReset ? json({ error: "disk full" }, 500) : json({ status: "ok" });
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

  it("edit changes the overlay and schedules the save", async () => {
    const { puts } = fakeServer();
    await load();
    edit((o) => { o.external_url = "https://x.example"; });
    expect(puts).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(SAVE_DELAY + 10);
    expect(puts).toHaveLength(1);
    expect(puts[0].external_url).toBe("https://x.example");
  });

  it("a reset cancels the save still pending", async () => {
    const { puts, server } = fakeServer();
    await load();
    change({ snapshot: false });
    let release;
    server.holdReset = new Promise((r) => (release = r));
    const reset = resetSettings();
    await vi.advanceTimersByTimeAsync(SAVE_DELAY * 2); // the timer would fire while the reset is in flight
    release();
    await reset;
    expect(puts).toEqual([]);
    expect(app.data.custom).toBe(false);
    expect(app.overlay.notify.snapshot).toBe(true);
  });

  it("a refused reset still sends the change made before it", async () => {
    const { puts } = fakeServer({ refuseReset: true });
    await load();
    change({ snapshot: false });
    await resetSettings();
    await vi.advanceTimersByTimeAsync(SAVE_DELAY * 2);
    expect(puts.length).toBe(1);
    expect(app.status.kind).toBe("saved");
  });

  it("a reset during a save does not let the older settings be sent after it", async () => {
    const { puts, server } = fakeServer();
    await load();
    change({ snapshot: false });
    let release;
    server.hold = new Promise((r) => (release = r));
    await vi.advanceTimersByTimeAsync(SAVE_DELAY); // the save is in flight
    change({ clip: false }); // pending behind it
    let releaseReset;
    server.holdReset = new Promise((r) => (releaseReset = r));
    const reset = resetSettings();
    release();
    await vi.advanceTimersByTimeAsync(SAVE_DELAY * 2); // the timer the finished save re-armed would fire here
    releaseReset();
    await reset;
    expect(puts.length).toBe(1);
    expect(app.overlay.notify.clip).toBe(true);
  });

  it("a refused reset shows the failure", async () => {
    fakeServer({ refuseReset: true });
    await load();
    await resetSettings();
    expect(app.error.startsWith("Could not go back to config.yml:")).toBe(true);
    expect(app.error).toContain("disk full");
  });

  it("a reset after a save reloads the settings without the saved ones", async () => {
    fakeServer();
    await load();
    change({ snapshot: false });
    await flush();
    expect(app.data.custom).toBe(true); // saved: the page now offers the way back
    await resetSettings();
    expect(app.data.custom).toBe(false);
    expect(app.overlay.notify.snapshot).toBe(true);
    expect(app.error).toBe("");
  });

  it("leaving a page sends the pending change first", async () => {
    const { puts } = fakeServer();
    await load();
    let putsWhenLeft = -1;
    vi.stubGlobal("location", { set href(v) { putsWhenLeft = puts.length; } });
    change({ snapshot: false });
    await leave("classic");
    expect(putsWhenLeft).toBe(1); // the page is left only once the change is sent
  });

  it("leaving a page with nothing pending sends nothing", async () => {
    const { puts } = fakeServer();
    await load();
    let went = "";
    vi.stubGlobal("location", { set href(v) { went = v; } });
    await leave("setup");
    expect(puts).toEqual([]);
    expect(went).toBe("setup");
  });
});
