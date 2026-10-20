// The emojis of the camera picker: emojibase's names and keywords, in the interface's
// language, loaded only when the picker opens.

const DATA = {
  en: () => import("emojibase-data/en/compact.json"),
  de: () => import("emojibase-data/de/compact.json"),
  es: () => import("emojibase-data/es/compact.json"),
  fr: () => import("emojibase-data/fr/compact.json"),
  it: () => import("emojibase-data/it/compact.json"),
  pt: () => import("emojibase-data/pt/compact.json"),
};

// COMPONENT is emojibase's group of the skin tones and hair styles: not emojis on their own.
const COMPONENT = 2;

// loadEmojis gives every emoji with its name in lang; English for a language emojibase lacks (sl).
export async function loadEmojis(lang) {
  const m = await (DATA[lang] ?? DATA.en)();
  return m.default.filter((e) => e.group !== COMPONENT).map((e) => ({ unicode: e.unicode, label: e.label, tags: e.tags || [] }));
}

// sameEmoji compares two emojis without their variation selector (🏚 and 🏚️ are one).
export const sameEmoji = (a, b) => a.replace(/️/g, "") === b.replace(/️/g, "");

const fold = (s) => s.normalize("NFD").replace(/\p{M}/gu, "").toLowerCase();

// searchEmojis keeps the emojis whose name or a keyword holds query, accents and case aside.
export function searchEmojis(list, query) {
  const q = fold(query.trim());
  if (!q) return list;
  return list.filter((e) => fold(e.label).includes(q) || e.tags.some((t) => fold(t).includes(q)));
}
