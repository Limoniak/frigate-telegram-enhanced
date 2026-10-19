// The hash routes of the main interface: #screen, #cameras/<name> for one camera, #activity/<hour> for one hour.
const SCREENS = ["overview", "style", "when", "cameras", "activity", "advanced"];
// Old links: the recipients now live on the When screen.
const MOVED = { recipients: "when" };

export function parseRoute(hash) {
  const [first, ...rest] = String(hash || "").replace(/^#/, "").split("/");
  const head = MOVED[first] ?? first;
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
