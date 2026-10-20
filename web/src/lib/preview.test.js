import { describe, expect, it } from "vitest";
import { previewModel } from "./preview.js";

describe("previewModel", () => {
  it("photo + video in place: the video replaces the image", () => {
    const m = previewModel({ snapshot: true, clip: true, gif: false, media_in_place: true, crop: true, group: "2m" });
    expect(m).toMatchObject({ photo: true, crop: true, inMessage: "video", reply: null, grouped: true });
  });
  it("photo + GIF as a reply when not in place", () => {
    expect(previewModel({ snapshot: true, gif: true, media_in_place: false })).toMatchObject({ inMessage: null, reply: "gif" });
  });
  it("text only: no image, no media", () => {
    expect(previewModel({ snapshot: false, clip: false, gif: false })).toMatchObject({ photo: false, inMessage: null, reply: null, crop: false });
  });
  it("a video without image arrives as a reply", () => {
    expect(previewModel({ snapshot: false, clip: true })).toMatchObject({ reply: "video" });
  });
  it("carries the GenAI description and the Home Assistant button", () => {
    expect(previewModel({ genai_description: true }, { homeAssistant: true })).toMatchObject({ genai: true, homeAssistant: true });
  });
  it("shows every part of the caption unless turned off, and the camera's emoji", () => {
    expect(previewModel({})).toMatchObject({ zones: true, score: true, link: true, date: true, emoji: "" });
    expect(previewModel({ show_zones: false, show_score: false, show_link: false, show_date: false, emoji: "🚪" }))
      .toMatchObject({ zones: false, score: false, link: false, date: false, emoji: "🚪" });
  });
});
