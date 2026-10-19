// A label as the notifications show it: LABELS (from i18n.js, shared with Go) gives its
// emoji and name; a label missing there keeps its name, after 🔊 for a sound and 🔔
// for an object. i18n.js declares LABELS with const in a classic script: a global
// binding, not a property of globalThis, hence the bare name behind typeof.
/* global LABELS */
const labels = typeof LABELS !== "undefined" ? LABELS : {};

export function labelText(label, sound, T) {
  const x = labels[label];
  if (x) return x.emoji + " " + T(x.name);
  return (sound ? "🔊 " : "🔔 ") + (label || T("Detection"));
}

export const labelEmoji = (label, sound) => labels[label]?.emoji ?? (sound ? "🔊" : "🔔");
