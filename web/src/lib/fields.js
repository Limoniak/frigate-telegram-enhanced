// The detailed settings, described once and shown twice (global and per camera), plus the
// checks and summaries around them. T and Tf are passed in so the module stays pure.
import { effective, styleOf } from "./settings.js";
import { untilText } from "./time.js";

const clone = (v) => JSON.parse(JSON.stringify(v));

export const DUR = /^(\d+(\.\d+)?(ns|us|ms|s|m|h))+$/;
export const validDuration = (s) => DUR.test(String(s).trim());
export const validURL = (s) => s === "" || /^https?:\/\/[^\s/]+/i.test(s);

export function clampScore(v) {
  const n = Number(v);
  if (!isFinite(n)) return 0;
  return Math.min(1, Math.max(0, Math.round(n * 100) / 100));
}

export const FIELDS = (T, data) => [
  { key: "enabled", type: "bool", name: T("Notifications enabled"),
    hint: T("Unchecked: no notifications at all (or none for this camera).") },
  { key: "chats", type: "chips", name: T("Recipients"), source: "chats",
    hint: data && data.can_connection ? T("Chats chosen on the Connection page (link at the bottom).")
      : T("Chats declared in telegram.chats (config.yml).") },
  { key: "labels", type: "chips", name: T("Objects"), source: "labels", free: true,
    hint: T("No object selected = every object is reported.") },
  { key: "zones", type: "chips", name: T("Zones"), source: "zones", free: true,
    hint: T("No zone selected = no constraint. Otherwise the object must have entered one of the zones.") },
  { key: "min_score", type: "score", name: T("Minimum score"), source: "labels" },
  { key: "cooldown", type: "duration", name: T("Delay between two notifications"),
    hint: T("Per camera and per object. E.g. 60s, 5m, 1h30m.") },
  { key: "ignore_sub_labels", type: "chips", source: "sub_labels", free: true,
    name: T("Don't notify for these labels"),
    hint: T("Frigate's labels (custom classification, faces, plates), e.g. your own car or cat.") },
  { key: "ignore_known", type: "bool", name: T("Only unknown objects"),
    hint: T("Every object Frigate recognizes (with a label) is ignored.") },
  { key: "sub_label_wait", type: "duration", name: T("Wait for the label"),
    hint: T("With a label filter, how long a notification waits for Frigate to recognize the object (e.g. 5s). Without a label by then, the object is unknown.") },
  { key: "ignore_stationary", type: "bool", name: T("Ignore stationary objects"),
    hint: T("Parked cars, garden furniture…") },
  { key: "severity", type: "chips", name: T("Severity"), source: "severity", mode: "reviews",
    hint: T("Which Frigate reviews trigger a notification.") },
  { key: "snapshot", type: "bool", name: T("Send the image") },
  { key: "crop", type: "bool", name: T("Crop the image on the object"),
    hint: T("Much more readable on a phone. Applies to event snapshots.") },
  { key: "clip", type: "bool", name: T("Send the video clip") },
  { key: "media_in_place", type: "bool", name: T("Video in the same message"),
    hint: T("The clip (or GIF) replaces the image in the notification, instead of arriving as a new message.") },
  { key: "gif", type: "bool", name: T("Send an animated GIF") },
  { key: "compress_clips", type: "bool", globalOnly: true, name: T("Compress clips over 50 MB"),
    hint: T("Telegram refuses videos over 50 MB: they are re-encoded at a lower quality instead of being sent as a link. Uses the server's CPU for a few seconds to a few minutes.") },
  { key: "genai_description", type: "bool", name: T("Add the GenAI description"),
    hint: T("When Frigate publishes it, it is added to the notification's caption.") },
  { key: "group", type: "duration", globalOnly: true, name: T("Group bursts over"),
    hint: T("0s = one message per detection. Applies to every camera.") },
  { key: "clip_delay", type: "duration", name: T("Wait before fetching the clip"),
    hint: T("Gives Frigate time to finish the recording.") },
  { key: "when_home", type: "choice", name: T("When someone is home"), presence: true,
    choices: [["notify", T("Notify as usual")], ["silent", T("Silent")], ["skip", T("Nothing")]] },
  { key: "quiet_hours", type: "ranges", name: T("Quiet hours"),
    hint: T("Notifications sent without sound.") },
  { key: "off_hours", type: "ranges", name: T("Off hours"),
    hint: T("No notifications during these ranges.") },
];

// The detailed settings, sorted by theme; `links` marks the group of the addresses
// (global only), which is not a notification setting.
export const GROUPS = (T) => [
  { title: T("Recipients and filtering"),
    keys: ["enabled", "chats", "labels", "zones", "min_score", "ignore_stationary", "severity"] },
  { title: T("Labels"), keys: ["ignore_sub_labels", "ignore_known", "sub_label_wait"] },
  { title: T("Media"),
    keys: ["snapshot", "crop", "clip", "media_in_place", "gif", "compress_clips", "clip_delay", "genai_description"] },
  { title: T("Pace"), keys: ["cooldown", "group"] },
  { title: T("Schedule and presence"), keys: ["when_home", "quiet_hours", "off_hours"] },
  { title: T("Links"), keys: [], links: true, globalOnly: true },
];

// fieldsFor are the fields to show, globally or for one camera. A camera has no
// switch field (its own box is the switch) and no global-only field.
export const fieldsFor = (data, camera, T) =>
  FIELDS(T, data).filter((f) =>
    (!f.mode || f.mode === data.mode) && (!f.presence || data.presence) &&
    !(camera && (f.globalOnly || f.key === "enabled")));

// Empty value per type. It serves when the global level has nothing to inherit:
// sending null would mean overriding nothing at all, since the server treats null as "missing".
export const EMPTY = { bool: false, chips: [], ranges: [], duration: "0s", score: { default: 0 }, choice: "skip" };

export const defaultFor = (field, inherited) =>
  clone(inherited === undefined || inherited === null ? EMPTY[field.type] : inherited);

// unionOf gathers the values offered: those of the camera, or those of every
// camera for the default settings.
export function unionOf(data, kind, camera) {
  const cams = camera ? (data.cameras || []).filter((c) => c.name === camera) : (data.cameras || []);
  const out = [];
  for (const c of cams) for (const v of c[kind] || []) if (!out.includes(v)) out.push(v);
  return out.sort();
}

export function optionsFor(data, field, camera) {
  switch (field.source) {
    case "chats": return data.chats || [];
    case "severity": return ["alert", "detection"];
    case "labels": return unionOf(data, "labels", camera);
    case "zones": return unionOf(data, "zones", camera);
    case "sub_labels": return (data.sub_labels || []).slice().sort();
    default: return [];
  }
}

// untracked lists the requested objects Frigate does not track on the camera. Empty
// if Frigate did not give its list (unreachable): we don't know, we say nothing.
export function untracked(data, overlay, name) {
  const info = (data.cameras || []).find((c) => c.name === name);
  if (!info || !info.labels || !info.labels.length) return [];
  return (effective(overlay, name).labels || []).filter((l) => !info.labels.includes(l));
}

// sensitivityEffect describes, on the recent activity, the effect of a minimum score.
export function sensitivityEffect(history, threshold, T, Tf) {
  const scored = (history || []).filter((e) => e.score > 0);
  if (!scored.length) {
    return T("Recent activity will show the effect of this setting once there are detections.");
  }
  const dropped = scored.filter((e) => e.score < threshold);
  const lost = dropped.filter((e) => e.sent).length;
  if (!dropped.length) {
    return Tf("None of the last %d detections would be ignored for a low score.", scored.length);
  }
  if (lost) {
    return Tf("Out of the last %d detections, %d would be ignored for a low score, including %d that were sent.",
      scored.length, dropped.length, lost);
  }
  return Tf("Out of the last %d detections, %d would be ignored for a low score.", scored.length, dropped.length);
}

// Reason for a filtering in plain words; `fix` tells whether a camera setting can do something about it.
export const REASONS = (T) => ({
  camera_disabled: { text: T("camera disabled"), fix: true },
  false_positive: { text: T("false positive according to Frigate") },
  stationary: { text: T("stationary object"), fix: true },
  severity: { text: T("severity not selected"), fix: true },
  label: { text: T("object type not selected"), fix: true },
  score: { text: T("score too low"), fix: true },
  zone: { text: T("outside the chosen zones"), fix: true },
  paused: { text: T("notifications paused") },
  muted: { text: T("camera muted") },
  home: { text: T("someone was home"), fix: true },
  recipients: { text: T("no recipient for this object or at this time") },
  sub_label: { text: T("ignored label"), fix: true },
  off_hours: { text: T("during off hours"), fix: true },
  cooldown: { text: T("already reported a moment ago"), fix: true },
});

// cameraSummary is the one-line summary of a camera: style, objects, zones, and what
// deserves attention (mute, object Frigate does not track). ctx = {tz, lang, T}.
export function cameraSummary(data, overlay, live, name, ctx) {
  const { T } = ctx;
  const eff = effective(overlay, name);
  if (eff.enabled === false) return T("disabled");
  const n = Object.keys(overlay.cameras[name]).length;
  const style = styleOf(eff);
  const bits = [style === "clip" ? T("Photo + video") : style === "photo" ? T("Photo only")
    : style === "gif" ? T("Photo + GIF") : style === "text" ? T("Text only") : T("custom style")];
  const labels = eff.labels || [];
  bits.push(labels.length ? labels.join(", ") : T("all objects"));
  if ((eff.zones || []).length) bits.push("📍 " + eff.zones.join(", "));
  if (!n) bits.push(T("default"));
  if (untracked(data, overlay, name).length) bits.unshift(T("⚠️ object not tracked by Frigate"));
  const mute = live && live.mutes.find((m) => m.camera === name);
  if (mute) bits.unshift(T("🔇 muted ") + untilText(mute.until, ctx));
  return bits.join(" · ");
}
