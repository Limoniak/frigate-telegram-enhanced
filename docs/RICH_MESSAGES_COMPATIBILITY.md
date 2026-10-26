# Telegram Rich Messages: compatibility

**Review date:** 2026-10-27

## Telegram Bot API

- Rich text requires Bot API 10.1, media blocks 10.2, and rich button blocks 10.3 according to Telegram's current changelog.
- The app uses `sendRichMessage` for outgoing text/media and `editMessageText` with `rich_message` for text edits and in-place media replacement. Older self-hosted Bot API servers may reject these methods/fields; there is no version negotiation or legacy wire fallback.
- Existing Go call sites (`SendMessage`, `SendPhoto`, `SendVideo`) remain, while their wire transport uses Rich Messages. This preserves app-level callers, not compatibility with old Bot API servers.
- The button serializer retains callback payloads, URL actions, row grouping, and left alignment. Telegram clients decide sizing and color presentation; fixed widths are not available through the API.
- `allow_sending_without_reply` only affects whether a message can be sent when the reply target is missing. `/snapshot` picker output is no longer a reply; notification snapshot callbacks retain the original reply behavior.

## Configuration and deployment

- No public configuration key or persistence format changes.
- Frigate, MQTT, Home Assistant, chat selection, filters, pause state, and existing callback payloads remain unchanged.
- The new `/snapshot` picker callback is `S:<camera>`. Existing `s:<camera>` callbacks continue to parse and reply to the triggering event. Old persisted callbacks are not stored, but remain accepted.
- The feature does not alter Docker packaging. Docker build was not run locally; existing CI includes Linux race tests and Docker image build.

## Localization and PR #48

- The feature branch does not contain PR #48 commits or `ru.json`.
- It adds a translated menu description and pause label keys to the six existing catalogs so the feature branch remains catalog-complete. A temporary Russian integration copy passed after applying the PR #48 catalog and the matching new translations; that copy is not a branch or PR and was not published.
- After PR #48 merges, reconcile overlapping pause-label/catalog changes during rebase. Keep Russian localization history in PR #48 and Rich Messages in its separate PR.

## Runtime limitations

- Media edit state is in memory and is lost on restart.
- A lost edit response has an uncertain outcome; the implementation avoids duplicate fallback sends, which can leave local media ID state unknown.
- HTML-to-rich conversion supports the markup currently emitted by the app, not arbitrary HTML.
- Real cloud/self-hosted Bot API and physical mobile-client compatibility remain unverified in this pass.
