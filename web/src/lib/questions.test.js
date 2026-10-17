import { describe, expect, it } from "vitest";
import { DAY, NIGHT, QUESTIONS, RECIPIENT_OBJECTS, RECIPIENT_WHEN, questionsFor, recipientValues, setRecipient } from "./questions.js";

const T = (s) => s;
const ids = (qs) => qs.map((q) => q.id);

describe("questions", () => {
  it("keeps the questions of the mode and of the presence", () => {
    const reviews = ids(questionsFor({ mode: "reviews", presence: false }, T));
    expect(reviews).toContain("severity");
    expect(reviews).not.toContain("home");
    const events = ids(questionsFor({ mode: "events", presence: true }, T));
    expect(events).toContain("home");
    expect(events).not.toContain("severity");
  });
  it("has the expected patches", () => {
    const opt = (id, label) => QUESTIONS(T).find((q) => q.id === id).options.find((o) => o.label === label);
    expect(opt("objects", "🚶🚗 People and cars").patch).toEqual({ labels: ["car", "person"] });
    expect(opt("night", "🌙 No notifications").patch).toEqual({ quiet_hours: [], off_hours: NIGHT });
    expect(DAY).toEqual([{ from: "07:00", to: "22:00" }]);
    expect(RECIPIENT_OBJECTS(T)).toHaveLength(3);
    expect(RECIPIENT_WHEN(T)[1].patch).toEqual({ quiet_hours: [], off_hours: DAY });
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
});
