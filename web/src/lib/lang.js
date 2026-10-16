// Pure language helpers, shared by the pages; i18n.svelte.js makes them reactive.
export const LANG_KEY = "frigate-telegram-enhanced-lang";

// browserLang is the first language of the browser that has a catalog, else English.
export function browserLang(prefs, codes) {
  for (const pref of prefs) {
    const code = String(pref).toLowerCase().split("-")[0];
    if (codes.includes(code)) return code;
  }
  return "en";
}

// savedChoice is the language chosen in the menu, or "auto" (nothing saved): the browser's.
export function savedChoice(storage, codes) {
  try {
    const saved = storage?.getItem(LANG_KEY);
    if (codes.includes(saved)) return saved;
  } catch { /* storage unavailable: follow the browser */ }
  return "auto";
}

// translate gives the text in lang, or the English text when the catalog has none.
export function translate(catalogs, lang, s) {
  return (catalogs[lang] && catalogs[lang][s]) || s;
}

// format fills the %s and %d of a translated text, in order.
export function format(text, args) {
  let i = 0;
  return text.replace(/%[sd]/g, () => String(args[i++]));
}
