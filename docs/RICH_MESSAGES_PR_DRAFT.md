# Rich Messages for Telegram notifications

## Summary

- Route existing text and media notifications through Telegram Rich Messages.
- Replace an event snapshot with its video or animation in the same Telegram message, preserving the caption and callback controls.
- Keep existing callback actions and improve the `/menu` section spacing and `/snapshot` camera-picker reply behavior.

## Motivation

Rich Messages provide structured text, media, and button blocks. They let the bot keep event context and controls together while updating a notification as Frigate finishes preparing a clip.

## Implementation details

- Adapt existing Telegram send methods to `sendRichMessage` without changing their Go-level call sites.
- Convert the app's supported HTML formatting into rich text and serialize media/button blocks.
- Use `editMessageText.rich_message` for same-message media replacement and text/menu edits.
- Serialize edits per tracked message and recipient; avoid duplicate fallback sends when an edit outcome is ambiguous.
- Keep legacy `s:<camera>` snapshot callbacks and add a separate callback for `/snapshot` camera selection so picker results are standalone messages.
- Render menu headings and pause labels through the existing localization catalogs.

## Backward compatibility

No public configuration or persistence format changes. Existing action payloads remain accepted. The full feature requires Telegram Bot API 10.3 for rich button blocks; older self-hosted Bot API servers need an upgrade.

## Tests

- `go test ./...` — PASS.
- `go vet ./...` — PASS.
- `git diff --check` — PASS.
- `go test -race ./...` — blocked locally because Windows CGO is unavailable; existing CI runs this check on Linux.
- Isolated Russian `/menu` mock integration — PASS.
- Rich API requests are tested with `httptest`; no real Telegram messages were sent.

## Known limitations

- HTML conversion supports only markup emitted by the app.
- Ambiguous media-edit timeouts can leave the current media `file_id` unknown; duplicate fallback sends are avoided.
- In-flight media state is not restored after a process restart.
- No live Telegram cloud/self-hosted API, physical mobile layout, Docker build, or real large-file upload was checked locally.
- Keep this PR separate from Russian localization PR #48; reconcile overlapping catalog changes when rebasing after it merges.

## Screenshots / checklist

- [ ] Attach a dedicated test-client screenshot of a rich notification with a snapshot, caption, and buttons.
- [ ] Attach a screenshot after the snapshot is replaced by a video in the same message.
- [ ] Confirm callbacks, `/menu`, and `/snapshot` with a dedicated test bot.
- [ ] Confirm the target Bot API server is 10.3 or newer.
- [ ] Confirm upstream CI, including race tests and Docker image build, passes.
- [ ] Confirm no configuration migration is needed.
