// The hash routes of the main interface: #screen, and #cameras/<name> for one camera.
const SCREENS = ["overview", "style", "when", "recipients", "cameras", "activity", "advanced"];

export function parseRoute(hash) {
  const [head, ...rest] = String(hash || "").replace(/^#/, "").split("/");
  const screen = SCREENS.includes(head) ? head : "overview";
  let camera = null;
  if (screen === "cameras" && rest.length) {
    try { camera = decodeURIComponent(rest.join("/")); } catch { camera = null; }
  }
  return { screen, camera };
}

export function routeHash({ screen, camera }) {
  return "#" + screen + (camera ? "/" + encodeURIComponent(camera) : "");
}
