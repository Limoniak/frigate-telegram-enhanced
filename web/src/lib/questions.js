// The questions of the simple settings screen and the choices offered to each recipient.
// T is passed in so the module stays pure.
import { matches, same } from "./settings.js";

// The night of the When screen, when none is saved (overlay.night).
export const DEFAULT_NIGHT = { from: "22:00", to: "07:00" };
export const nightOf = (overlay) => overlay.night || DEFAULT_NIGHT;
const dayOf = (night) => ({ from: night.to, to: night.from });

const clone = (v) => JSON.parse(JSON.stringify(v));

// QUESTIONS are the choices given as pills; the objects to report are chips (Objects.svelte).
export const QUESTIONS = (T, night = DEFAULT_NIGHT) => [
  { id: "severity", title: T("Which reviews?"), mode: "reviews", options: [
    { label: T("Alerts only"), patch: { severity: ["alert"] } },
    { label: T("Alerts and detections"), patch: { severity: ["alert", "detection"] } },
  ] },
  { id: "cooldown", title: T("How often, at most?"),
    hint: T("Per camera and per object type: further detections are ignored during this delay."),
    options: [
      { label: T("Every detection"), patch: { cooldown: "0s" } },
      { label: T("1 per minute"), patch: { cooldown: "1m" } },
      { label: T("1 every 10 min"), patch: { cooldown: "10m" } },
    ] },
  { id: "group", title: T("Bursts"), more: true,
    hint: T("Detections close in time are added to the first message (edited, no new sound) instead of sending new ones."),
    options: [
      { label: T("One message per detection"), patch: { group: "0s" } },
      { label: T("📦 Group over 2 min"), patch: { group: "2m" } },
      { label: T("📦 Group over 5 min"), patch: { group: "5m" } },
    ] },
  { id: "home", title: T("When someone is home"), presence: true,
    hint: T("Someone is home as soon as one of the presence topics says so (Home Assistant, sensor…)."),
    options: [
      { label: T("🔔 Notify as usual"), patch: { when_home: "notify" } },
      { label: T("🔕 Silent"), patch: { when_home: "silent" } },
      { label: T("🏠 Nothing"), patch: { when_home: "skip" } },
    ] },
  { id: "night", title: T("At night"), options: [
    { label: T("Same as daytime"), patch: { quiet_hours: [], off_hours: [] } },
    { label: T("🔕 Silent"), patch: { quiet_hours: [night], off_hours: [] } },
    { label: T("🌙 No notifications"), patch: { quiet_hours: [], off_hours: [night] } },
  ] },
];

// questionsFor keeps the questions that apply: those of the current mode, and the
// presence one only when presence topics are configured.
export const questionsFor = (data, T, night) =>
  QUESTIONS(T, night).filter((q) => (!q.mode || q.mode === data.mode) && (!q.presence || data.presence));

// A recipient's hours only narrow the global ones: the first choice keeps them as is.
export const RECIPIENT_WHEN = (T, night = DEFAULT_NIGHT) => [
  { label: T("Same as everyone"), patch: { quiet_hours: [], off_hours: [] } },
  { label: T("🌙 Only at night"), patch: { quiet_hours: [], off_hours: [dayOf(night)] } },
  { label: T("☀️ Only during the day"), patch: { quiet_hours: [], off_hours: [night] } },
  { label: T("🔕 Silent at night"), patch: { quiet_hours: [night], off_hours: [] } },
];

// recipientHours keeps the recipient's hour choices that still change something under
// the global settings (notify), plus the active one, so that a saved choice stays shown.
export function recipientHours(notify, values, T, night = DEFAULT_NIGHT) {
  const offNight = matches({ off_hours: [night] }, notify);
  const quietNight = matches({ quiet_hours: [night] }, notify);
  // Off at night for everyone leaves no night to keep or to silence, nor a day to cut.
  const useful = (p) => (!p.off_hours.length && !p.quiet_hours.length) || (!offNight && !(p.quiet_hours.length && quietNight));
  return RECIPIENT_WHEN(T, night).filter((o) => useful(o.patch) || matches(o.patch, values));
}

// setNight changes the night: the hours that were the old night (or its day) become the
// new one's, everywhere, so that the choices made keep their meaning.
export function setNight(overlay, night) {
  const old = nightOf(overlay);
  const swap = (ranges) => (ranges || []).map((r) =>
    same("off_hours", [r], [old]) ? { ...night } : same("off_hours", [r], [dayOf(old)]) ? dayOf(night) : r);
  const levels = [overlay.notify, ...Object.values(overlay.cameras), ...Object.values(overlay.recipients)];
  for (const l of levels) for (const k of ["quiet_hours", "off_hours"]) if (l[k]) l[k] = swap(l[k]);
  overlay.night = { ...night };
}

export function recipientValues(overlay, chat) {
  const r = overlay.recipients[chat] || {};
  return { labels: r.labels || [], quiet_hours: r.quiet_hours || [], off_hours: r.off_hours || [] };
}

// setRecipient applies a choice to a recipient; empty lists are left out, and a
// recipient with nothing left is removed (it receives everything).
export function setRecipient(overlay, chat, patch) {
  const r = Object.assign(recipientValues(overlay, chat), clone(patch));
  for (const k of Object.keys(r)) if (!r[k].length) delete r[k];
  if (Object.keys(r).length) overlay.recipients[chat] = r;
  else delete overlay.recipients[chat];
}
