// Times as the interface shows them: in the service's time zone and the page's
// language.
export const locale = (lang) => (lang === "en" ? "en-GB" : lang);

const day = (d, tz, lang) => d.toLocaleDateString(locale(lang), { timeZone: tz });

export function timeText(iso, { tz, lang }) {
  return new Date(iso).toLocaleTimeString(locale(lang), { timeZone: tz, hour: "2-digit", minute: "2-digit" });
}

// hourStart is the start, in Unix seconds, of the chart's bar i (0..23, 23 the last
// hour) when the chart was fetched at at (ms). The server counts bar i over
// (now-(24-i)h, now-(23-i)h] of the notifications' send times: windows that end now,
// not on the clock's hours, so that the activity filter, which takes the same window
// on the same instants, shows what the bar counted.
export const hourStart = (at, i) => Math.floor(at / 1000) - (24 - i) * 3600;

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
