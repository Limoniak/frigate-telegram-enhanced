// The activity's lists, with the windows of the overview's chart.

// inHour: whether a detection falls in the window [hour, hour + 1 h), hour in Unix
// seconds, null for no window. The chart counts the notifications at the time they
// were sent (sent_at; older files only have the event's start), an ignored detection
// has only its start.
export function inHour(e, hour) {
  if (!hour) return true;
  const t = Date.parse((e.sent && e.sent_at) || e.at);
  return t >= hour * 1000 && t < (hour + 3600) * 1000;
}

// The history keeps this many detections, the most recent first.
export const KEPT = 50;

// tooOld: the hour begins before the oldest detection kept, so a bar may count
// notifications the list no longer has.
export const tooOld = (history, hour) => !!hour && history.length >= KEPT && hour * 1000 < Date.parse(history.at(-1).at);
