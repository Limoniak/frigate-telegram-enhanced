// The state of the main interface, shared by its screens: the settings (saved by
// themselves shortly after a change, as ui.js did), the live state (pause, mutes),
// the connections and the recent activity.
import { api as rawApi, post as rawPost } from "./api.js";
import { T, i18n } from "./i18n.svelte.js";
import { apply, toSend } from "./settings.js";

export const SAVE_DELAY = 600;

export const app = $state({
  data: null, overlay: null, live: null, health: null, refused: [], history: null, activity: null,
  status: { text: "", kind: "" }, error: "", warning: "",
});

const ctx = () => ({ lang: i18n.lang, next: "" });
const api = (path, opts) => rawApi(path, opts, ctx());
const post = (path, body, method) => rawPost(path, body, { ...ctx(), method });

let saved = ""; // the overlay as last saved, to tell what changed
let saveTimer = null;
let saving = null; // the save in progress: saves never overlap

const dirty = () => app.overlay !== null && JSON.stringify(app.overlay) !== saved;

export async function load() {
  const r = await api("api/settings", { headers: { Accept: "application/json" } });
  if (!r.ok) throw new Error("HTTP " + r.status);
  const d = await r.json();
  const o = d.overlay || {};
  const overlay = {
    notify: o.notify || {}, cameras: o.cameras || {}, recipients: o.recipients || {},
    external_url: o.external_url || "", home_assistant_url: o.home_assistant_url || "",
  };
  // Every known camera gets an entry (empty = follows the global level) before the
  // saved state is recorded, or the page would think it changed on opening.
  for (const c of d.cameras || []) overlay.cameras[c.name] ??= {};
  saved = JSON.stringify(overlay);
  app.data = d;
  app.overlay = overlay;
  app.warning = d.warning || "";
  await Promise.all([d.can_pause && refreshState(), d.can_history && refreshHistory()]);
  if (d.can_health) refreshHealth(); // may take a few seconds (MQTT probe): not awaited
}

// change applies a choice, globally or to one camera, and schedules the save.
export function change(patch, camera) {
  apply(app.overlay, patch, camera);
  touched();
}

// edit applies a free-form change to the overlay (recipients, addresses), and schedules the save.
export function edit(fn) {
  fn(app.overlay);
  touched();
}

function touched() {
  if (!dirty()) return;
  app.status = { text: T("Saving…"), kind: "dirty" };
  clearTimeout(saveTimer);
  saveTimer = setTimeout(save, SAVE_DELAY);
}

// save sends the settings if they changed; false when the server refused them.
export async function save() {
  clearTimeout(saveTimer);
  while (saving) await saving;
  if (!dirty()) return true;
  const sent = JSON.stringify(app.overlay);
  let ok = false;
  saving = (async () => {
    try {
      const r = await post("api/settings", toSend(app.overlay), "PUT");
      if (r.ok) {
        saved = sent;
        ok = true;
        app.error = "";
        if (!app.data.custom) app.data.custom = true;
        app.status = { text: T("Saved ✓ — applied immediately"), kind: "saved" };
      } else {
        app.error = T("Save refused:\n") + (r.data.error || "HTTP " + r.status);
        app.status = { text: T("Not saved"), kind: "dirty" };
      }
    } catch (e) {
      app.error = T("Save refused:\n") + e.message;
      app.status = { text: T("Not saved"), kind: "dirty" };
    }
  })();
  try { await saving; } finally { saving = null; }
  if (ok && dirty()) touched(); // changed while it was being sent: one more save
  return ok;
}

// flush saves what is pending right away; false when the settings were refused.
export async function flush() {
  if (saving) await saving;
  return dirty() ? save() : true;
}

async function getJSON(path) {
  const r = await api(path);
  return r.ok ? r.json() : null;
}

export async function refreshState() {
  const v = await getJSON("api/state").catch(() => null);
  if (v) app.live = v;
}

export async function refreshHealth() {
  const v = await getJSON("api/health").catch(() => null);
  if (!v) return;
  app.health = v.components;
  app.refused = v.refused || [];
}

export async function refreshHistory() {
  const v = await getJSON("api/history").catch(() => null);
  if (!v) return;
  app.history = v.entries || [];
  app.activity = v.sent_hours ? { sent: v.sent_24h, hours: v.sent_hours } : null;
}

// resetSettings goes back to config.yml (drops the saved settings), then reloads.
export async function resetSettings() {
  clearTimeout(saveTimer); // a pending save must not land after the reset and undo it
  let done = false; // the server went back: the change made before must not be sent any more
  try {
    if (saving) await saving;
    clearTimeout(saveTimer); // the save just finished may have re-armed it
    const r = await post("api/settings/reset");
    if (!r.ok) throw new Error(r.data.error || "HTTP " + r.status);
    done = true;
    await load();
    app.status = { text: "", kind: "" }; // the dropped change is no longer "Saving…"
  } catch (e) {
    app.error = T("Could not go back to config.yml:\n") + e.message;
    if (!done) touched(); // the change made before the reset is still to be sent
  }
}

// leave goes to another page once the pending change is sent.
export async function leave(href) {
  await flush();
  location.href = href;
}

async function stateCall(path, body) {
  try {
    const r = await post(path, body);
    if (r.ok) app.live = r.data;
    else app.error = T("Action failed:\n") + (r.data.error || "HTTP " + r.status);
  } catch (e) {
    app.error = T("Action failed:\n") + e.message;
  }
}

export const pause = (minutes) => stateCall("api/pause", { minutes });
export const resume = (camera) => stateCall("api/resume", camera ? { camera } : {});

// sendTest sends a sample notification with the saved settings: pending changes first.
export async function sendTest(camera) {
  if (!(await flush())) throw new Error(T("the settings could not be saved"));
  const r = await post("api/test", { camera });
  if (!r.ok) throw new Error(r.data.error || "HTTP " + r.status);
}

// startPolling refreshes what can change from Telegram or Frigate every 30 s, and
// sends a pending save when the page is left.
export function startPolling() {
  setInterval(() => {
    if (!app.data || document.hidden) return;
    if (app.data.can_pause) refreshState();
    if (app.data.can_history) refreshHistory();
    if (app.data.can_health) refreshHealth();
  }, 30000);
  addEventListener("beforeunload", (e) => {
    if (!dirty()) return;
    save();
    e.preventDefault();
  });
}
