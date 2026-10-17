// previewModel says what a notification looks like with the effective settings eff,
// as ui.js drew it: the media replaces the image when "in place", otherwise it comes
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
    homeAssistant,
  };
}
