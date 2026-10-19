import { describe, expect, it } from "vitest";
import { DEFAULT_NIGHT, QUESTIONS, RECIPIENT_WHEN, nightOf, questionsFor, recipientHours, recipientValues, setNight, setRecipient } from "./questions.js";

const T = (s) => s;
const ids = (qs) => qs.map((q) => q.id);
const NIGHT = [DEFAULT_NIGHT];
const DAY = [{ from: "07:00", to: "22:00" }];

describe("questions", () => {
  it("keeps the questions of the mode and of the presence", () => {
    const reviews = ids(questionsFor({ mode: "reviews", presence: false }, T));
    expect(reviews).toContain("severity");
    expect(reviews).not.toContain("home");
    const events = ids(questionsFor({ mode: "events", presence: true }, T));
    expect(events).toContain("home");
    expect(events).not.toContain("severity");
  });
  it("has the expected patches, on the night given", () => {
    const opt = (id, label, night) => QUESTIONS(T, night).find((q) => q.id === id).options.find((o) => o.label === label);
    expect(opt("night", "🌙 No notifications").patch).toEqual({ quiet_hours: [], off_hours: NIGHT });
    expect(opt("night", "🔕 Silent", { from: "23:30", to: "06:00" }).patch.quiet_hours).toEqual([{ from: "23:30", to: "06:00" }]);
    expect(RECIPIENT_WHEN(T)[1].patch).toEqual({ quiet_hours: [], off_hours: DAY });
  });
});

describe("night", () => {
  it("is 22:00-07:00 until one is saved", () => {
    expect(nightOf({})).toEqual({ from: "22:00", to: "07:00" });
    expect(nightOf({ night: { from: "00:00", to: "08:00" } })).toEqual({ from: "00:00", to: "08:00" });
  });
  it("moves the hours that were the old night or its day, and leaves the others", () => {
    const o = {
      notify: { quiet_hours: NIGHT, off_hours: [] },
      cameras: { garage: { off_hours: [{ from: "12:00", to: "13:00" }] } },
      recipients: { famille: { off_hours: DAY }, moi: { quiet_hours: NIGHT } },
    };
    setNight(o, { from: "23:00", to: "06:00" });
    expect(o.night).toEqual({ from: "23:00", to: "06:00" });
    expect(o.notify.quiet_hours).toEqual([{ from: "23:00", to: "06:00" }]);
    expect(o.recipients.famille.off_hours).toEqual([{ from: "06:00", to: "23:00" }]);
    expect(o.recipients.moi.quiet_hours).toEqual([{ from: "23:00", to: "06:00" }]);
    expect(o.cameras.garage.off_hours).toEqual([{ from: "12:00", to: "13:00" }]);
  });
});

describe("recipients", () => {
  it("has empty lists for an unknown recipient", () => {
    expect(recipientValues({ recipients: {} }, "famille")).toEqual({ labels: [], quiet_hours: [], off_hours: [] });
  });
  it("drops the recipient once nothing is left", () => {
    const o = { recipients: {} };
    setRecipient(o, "famille", { labels: ["person"] });
    expect(o.recipients.famille).toEqual({ labels: ["person"] });
    setRecipient(o, "famille", { labels: [] });
    expect(o.recipients).not.toHaveProperty("famille");
  });
  it("offers only the hours that narrow the global ones", () => {
    const none = { labels: [], quiet_hours: [], off_hours: [] };
    const labels = (c) => c.map((o) => o.label);
    expect(recipientHours({}, none, T)).toHaveLength(4);
    expect(labels(recipientHours({ off_hours: NIGHT }, none, T))).toEqual(["Same as everyone"]);
    expect(labels(recipientHours({ quiet_hours: NIGHT }, none, T))).not.toContain("🔕 Silent at night");
  });
  it("keeps a saved choice shown even when it no longer narrows", () => {
    const c = recipientHours({ off_hours: NIGHT }, { labels: [], quiet_hours: [], off_hours: DAY }, T);
    expect(c.map((o) => o.label)).toContain("🌙 Only at night");
  });
});
