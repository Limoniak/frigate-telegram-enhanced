// The settings logic of the interface: comparing values the way Go writes them, applying a
// choice globally or to one camera, and the notification styles. Pure: the store
// (store.svelte.js) makes it reactive.

const DURATIONS = new Set(["cooldown", "group", "sub_label_wait", "clip_delay"]);
const LISTS = new Set(["chats", "labels", "zones", "ignore_sub_labels", "severity"]);
const RANGES = new Set(["quiet_hours", "off_hours"]);
const BOOLS = new Set(["enabled", "ignore_known", "ignore_stationary", "snapshot", "crop", "clip", "media_in_place", "gif", "compress_clips", "outage_alerts", "genai_description"]);

const clone = (v) => JSON.parse(JSON.stringify(v));

// seconds converts a Go duration ("1m0s", "90s", "1h30m") into seconds.
export function seconds(d) {
  const unit = { ns: 1e-9, us: 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
  let total = 0;
  for (const [, n, u] of String(d || "0s").matchAll(/(\d+(?:\.\d+)?)(ns|us|ms|s|m|h)/g)) total += Number(n) * unit[u];
  return Math.round(total * 1000) / 1000;
}

function norm(key, v) {
  if (DURATIONS.has(key)) return seconds(v);
  if (LISTS.has(key)) return (v || []).slice().sort();
  if (RANGES.has(key)) return v || [];
  if (BOOLS.has(key)) return !!v;
  return v;
}

export const same = (key, a, b) => JSON.stringify(norm(key, a)) === JSON.stringify(norm(key, b));

export const matches = (patch, values) => Object.entries(patch).every(([k, v]) => same(k, v, values[k]));

// effective is the settings of the global level, or of a camera.
export const effective = (overlay, camera) =>
  camera ? { ...overlay.notify, ...overlay.cameras[camera] } : overlay.notify;

// apply sets a choice. For a camera, a choice equal to the global one removes the
// override rather than duplicating it: the camera then follows the global level.
export function apply(overlay, patch, camera) {
  if (!camera) {
    Object.assign(overlay.notify, clone(patch));
    return;
  }
  const cam = (overlay.cameras[camera] ??= {});
  if (matches(patch, overlay.notify)) for (const k of Object.keys(patch)) delete cam[k];
  else Object.assign(cam, clone(patch));
}

// toSend is the overlay as the server saves it: cameras without overrides follow the
// defaults and are left out.
export function toSend(overlay) {
  const cameras = {};
  for (const [name, patch] of Object.entries(overlay.cameras)) if (Object.keys(patch).length) cameras[name] = patch;
  return { notify: overlay.notify, cameras, recipients: overlay.recipients, external_url: overlay.external_url, home_assistant_url: overlay.home_assistant_url, frame_ancestors: overlay.frame_ancestors, night: overlay.night };
}

// STYLES are the notification styles of the Style screen, as patches of settings.
export const STYLES = [
  { id: "clip", patch: { snapshot: true, clip: true, gif: false } },
  { id: "photo", patch: { snapshot: true, clip: false, gif: false } },
  { id: "gif", patch: { snapshot: true, clip: false, gif: true } },
  { id: "text", patch: { snapshot: false, clip: false, gif: false } },
];

export const styleOf = (eff) => STYLES.find((s) => matches(s.patch, eff))?.id ?? "custom";

// cameraOverrides lists the cameras that set one of keys themselves.
export const cameraOverrides = (overlay, keys) =>
  Object.entries(overlay.cameras).filter(([, p]) => keys.some((k) => k in p)).map(([name]) => name).sort();

// inheritsAll tells whether the camera sets none of keys: it follows the global level.
export const inheritsAll = (overlay, keys, camera) => !keys.some((k) => k in (overlay.cameras[camera] || {}));

// clearKeys removes keys from the camera's overrides, which then follow the global level.
export function clearKeys(overlay, keys, camera) {
  const cam = overlay.cameras[camera];
  if (cam) for (const k of keys) delete cam[k];
}
