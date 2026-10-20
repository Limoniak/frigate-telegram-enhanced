// previewModel says what a notification looks like with the effective settings eff,
// as Telegram shows it: the media replaces the image when "in place", otherwise it comes
// as a reply.
import { seconds } from "./settings.js";

export function previewModel(eff, { homeAssistant = false } = {}) {
  const photo = !!eff.snapshot;
  const media = eff.clip ? "video" : eff.gif ? "gif" : null;
  const inPlace = eff.media_in_place !== false;
  return {
    photo,
    crop: photo && !!eff.crop,
    inMessage: photo && inPlace ? media : null,
    reply: media && !(photo && inPlace) ? media : null,
    grouped: seconds(eff.group) > 0,
    genai: !!eff.genai_description,
    // What the caption shows (on unless turned off), and the camera's emoji.
    zones: eff.show_zones !== false,
    score: eff.show_score !== false,
    link: eff.show_link !== false,
    date: eff.show_date !== false,
    emoji: eff.emoji || "",
    homeAssistant,
  };
}
