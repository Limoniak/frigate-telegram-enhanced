// Times as the interface shows them: in the service's time zone and the page's
// language.
// English follows the browser's variant (en-US: a 12-hour clock), British otherwise.
export const locale = (lang, prefs = globalThis.navigator?.languages ?? []) =>
  lang !== "en" ? lang : prefs.find((l) => /^en-/i.test(l)) ?? "en-GB";

const day = (d, tz, lang) => d.toLocaleDateString(locale(lang), { timeZone: tz });

export function timeText(iso, { tz, lang }) {
  return new Date(iso).toLocaleTimeString(locale(lang), { timeZone: tz, hour: "2-digit", minute: "2-digit" });
}

// hourStart is the start, in Unix seconds, of the chart's bar i (0..23, 23 the current
// hour), hour being the start of the current clock hour (seconds) as the server gives
// it: bar i covers [hourStart, hourStart + 1 h), so that the activity filter, which
// takes the same window, shows what the bar counted.
export const hourStart = (hour, i) => hour - (23 - i) * 3600;

// chartTicks are the bars that start at the clock's 00:00, 06:00, 12:00 or 18:00 in the
// time zone tz: t in Unix seconds and x, the share of the chart from its left edge.
export function chartTicks(hour, tz) {
  const clock = new Intl.DateTimeFormat("en-GB", { timeZone: tz, hour: "numeric", hourCycle: "h23" });
  const ticks = [];
  for (let i = 0; i < 24; i++) {
    const t = hourStart(hour, i);
    if (Number(clock.format(t * 1000)) % 6 === 0) ticks.push({ t, x: i / 24 });
  }
  return ticks;
}

// untilText is the end of a pause or a mute ("until 22:30"); null means until resumed.
export function untilText(iso, { tz, lang, T, now = new Date() }) {
  if (!iso) return T("until resumed");
  const d = new Date(iso);
  const opts = { timeZone: tz, hour: "2-digit", minute: "2-digit" };
  if (day(d, tz, lang) !== day(now, tz, lang)) Object.assign(opts, { weekday: "long", day: "numeric", month: "long" });
  return T("until ") + d.toLocaleString(locale(lang), opts);
}

// whenText is when something happened: the time today, "yesterday 21:00", or the date.
export function whenText(iso, { tz, lang, T, now = new Date() }) {
  const d = new Date(iso);
  const time = timeText(iso, { tz, lang });
  if (day(d, tz, lang) === day(now, tz, lang)) return time;
  if (day(d, tz, lang) === day(new Date(now.getTime() - 86400000), tz, lang)) return T("yesterday ") + time;
  return d.toLocaleDateString(locale(lang), { timeZone: tz, day: "numeric", month: "short" }) + " " + time;
}
