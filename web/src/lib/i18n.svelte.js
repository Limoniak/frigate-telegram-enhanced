// The page's language, reactive: T() re-renders what it translated when the
// language changes. LANGUAGES and CATALOGS come from i18n.js, served by Go.
import { LANG_KEY, browserLang, format, savedChoice, translate } from "./lang.js";

export const LANGUAGES = globalThis.LANGUAGES ?? [];
const CATALOGS = globalThis.CATALOGS ?? {};
const codes = LANGUAGES.map((l) => l.code);

function storage() {
  try { return globalThis.localStorage ?? null; } catch { return null; }
}
const fromBrowser = () => browserLang(globalThis.navigator?.languages ?? [globalThis.navigator?.language ?? ""], codes);

const choice = savedChoice(storage(), codes);
export const i18n = $state({ choice, lang: choice === "auto" ? fromBrowser() : choice });

export const T = (s) => translate(CATALOGS, i18n.lang, s);
export const Tf = (s, ...args) => format(T(s), args);

// setChoice applies a language, or "auto" to follow the browser (nothing saved).
export function setChoice(c) {
  i18n.choice = c;
  i18n.lang = c === "auto" ? fromBrowser() : c;
  try {
    if (c === "auto") storage()?.removeItem(LANG_KEY);
    else storage()?.setItem(LANG_KEY, c);
  } catch { /* not remembered */ }
}

globalThis.addEventListener?.("languagechange", () => { if (i18n.choice === "auto") setChoice("auto"); });
