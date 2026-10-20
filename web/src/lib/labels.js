// A label as the notifications show it: LABELS (from i18n.js, shared with Go) gives its
// emoji and name; a label missing there keeps its name, after 🔊 for a sound and 🔔
// for an object. i18n.js declares LABELS with const in a classic script: a global
// binding, not a property of globalThis, hence the bare name behind typeof.
/* global LABELS */
const labels = typeof LABELS !== "undefined" ? LABELS : {};

// A recognized object, as Frigate 0.16 writes it in a review ("car-verified"), is its
// object; the service now records it so, but older entries of the history may not.
const base = (label) => (label || "").replace(/-verified$/, "");

export function labelText(label, sound, T) {
  const x = labels[base(label)];
  if (x) return x.emoji + " " + T(x.name);
  return (sound ? "🔊 " : "🔔 ") + (base(label) || T("Detection"));
}

export const labelEmoji = (label, sound) => labels[base(label)]?.emoji ?? (sound ? "🔊" : "🔔");
