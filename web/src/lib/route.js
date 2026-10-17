// The hash routes of the main interface: #screen, #cameras/<name> for one camera, #activity/<hour> for one hour.
const SCREENS = ["overview", "style", "when", "recipients", "cameras", "activity", "advanced"];

export function parseRoute(hash) {
  const [head, ...rest] = String(hash || "").replace(/^#/, "").split("/");
  const screen = SCREENS.includes(head) ? head : "overview";
  let camera = null;
  if (screen === "cameras" && rest.length) {
    try { camera = decodeURIComponent(rest.join("/")); } catch { camera = null; }
  }
  // #activity/<unix seconds of the start of an hour>
  const n = screen === "activity" && /^\d+$/.test(rest[0] ?? "") ? Number(rest[0]) : null;
  return { screen, camera, hour: Number.isSafeInteger(n) ? n : null };
}

export function routeHash({ screen, camera, hour }) {
  return "#" + screen + (camera ? "/" + encodeURIComponent(camera) : "") + (hour ? "/" + hour : "");
}
