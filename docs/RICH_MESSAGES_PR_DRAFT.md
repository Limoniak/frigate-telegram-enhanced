# Rich Messages for Telegram notifications

## Summary

- Send notification text, photos, videos, GIFs, and inline controls through Telegram Rich Messages.
- Keep event context, captions, and callback actions together when replacing a snapshot with a clip.
- Preserve the existing media-in-place setting and reply behavior.

## Motivation

Telegram introduced structured Rich Messages, including media and button blocks. Using those blocks lets the bot keep the event caption and its controls with the media and update an existing notification as a clip becomes available.

## Implementation details

- Route the existing Telegram send methods through `sendRichMessage` while retaining their Go-level call sites.
- Convert the app's supported HTML formatting and entities into rich text values.
- Serialize photo, video, animation, and button blocks; reuse returned `file_id` values across recipients.
- Edit text and replace media with `editMessageText.rich_message`.
- Serialize edits per tracked message and recipient; avoid duplicate fallback messages when the edit result is ambiguous.
- Keep callback payloads and existing command/action handlers intact.

## Backward compatibility

No public configuration keys or persistence formats change. The Go client methods and notification options remain available. The Telegram wire API now requires Bot API 10.3 for the full feature set (Rich Messages 10.1, media 10.2, button blocks 10.3); older self-hosted Bot API servers must be upgraded.

## Tests

- `go test ./...` — PASS.
- `go vet ./...` — PASS.
- `go test -race ./...` — not run successfully in the local Windows environment because cgo and a C compiler are unavailable; please rely on CI for this gate.
- Telegram requests are tested with `httptest` and notifier behavior with fakes; no real Telegram messages were sent.

## Known limitations

- HTML conversion covers only tags emitted by this application.
- A timeout after an edit can leave the current media `file_id` unknown; the implementation avoids a potentially duplicated fallback.
- No live Telegram cloud/self-hosted API test or physical mobile-client test is included.
- This PR must stay separate from the Russian localization and UI changes in PR #48.

## Screenshots / checklist

- [ ] Attach a phone screenshot of a rich notification with snapshot, caption, and buttons.
- [ ] Attach a phone screenshot after the snapshot is replaced by a video.
- [ ] Confirm callback actions and `/menu` on a dedicated test bot.
- [ ] Confirm Bot API 10.3+ deployment and CI checks.
- [ ] Confirm no configuration migration is needed.
