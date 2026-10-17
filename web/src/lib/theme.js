// The theme choice: "auto" follows the system; "light" and "dark" force it through the
// data-theme attribute that app.css reads. The pages' <head> script applies it before
// the first paint.
export const THEME_KEY = "frigate-telegram-enhanced-theme";

// savedTheme is the choice kept in the browser, or "auto" (nothing saved or no storage).
export function savedTheme() {
  try {
    const saved = localStorage.getItem(THEME_KEY);
    if (saved === "light" || saved === "dark") return saved;
  } catch { /* storage unavailable: follow the system */ }
  return "auto";
}

// applyTheme shows the choice on the page and keeps it ("auto" forgets it).
export function applyTheme(choice) {
  const root = document.documentElement;
  if (choice === "light" || choice === "dark") root.setAttribute("data-theme", choice);
  else root.removeAttribute("data-theme");
  try {
    if (choice === "light" || choice === "dark") localStorage.setItem(THEME_KEY, choice);
    else localStorage.removeItem(THEME_KEY);
  } catch { /* storage unavailable: the choice lasts until the page closes */ }
}
