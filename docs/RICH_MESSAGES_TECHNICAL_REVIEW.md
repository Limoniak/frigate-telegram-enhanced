# Telegram Rich Messages: technical review

**Review date:** 2026-10-27
**Branch base:** upstream `dev` at `27646bd`
**Scope:** local Rich Messages implementation; no changes to PR #48.

## Summary

The implementation routes text notifications and menus through `sendRichMessage`, represents notification media and inline controls as rich blocks, and edits a media-bearing message through `editMessageText` with `rich_message`. The wire shapes match the current Bot API definitions for `InputRichBlockPhoto`, `InputRichBlockVideo`, `InputRichBlockAnimation`, and `InputRichBlockButtons`. Button rows preserve their logical grouping and serialize `align: "left"`; Telegram documents 1–8 buttons per row and supports `left`, `center`, or `right` alignment.

The release branch is based on current upstream `dev`, not PR #48. It contains no Russian catalog or localization/UI commits. No API call was made to a live bot during this review.

## Implementation flow

1. `internal/telegram/client.go` maps existing `SendMessage`, `SendPhoto`, `SendVideo`, and `SendAnimation` calls onto `sendRichMessage`. `SendRichMessage` validates that exactly one content form (HTML, Markdown, or blocks) is present.
2. Rich media is encoded as media blocks and uploaded with `multipart/form-data` using `attach://file`; returned rich media `file_id` values are extracted in `internal/telegram/types.go` for reuse across recipient chats.
3. Notification construction and delivery live in `internal/notifier/caption.go`, `media.go`, `events.go`, `reviews.go`, `ondemand.go`, and `group.go`. Photo, video, animation, text-only fallback, Frigate link, and controls share the same event context.
4. `EditRichMedia` sends a media block and caption through `editMessageText.rich_message`; the rich button block is appended in the same request. Text-only menu edits use the same rich text path.
5. Telegram callback payloads are still parsed by the existing action handler (`internal/actions/actions.go` / `internal/bot`). Pause, resume, snapshot, clip, camera, refresh, and Home Assistant URL behavior retain their established action data.

## Correctness and review findings

### Confirmed by code and deterministic tests

- HTML formatting and entities are converted into rich text values; Unicode and emoji survive conversion.
- Photo, video, and animation file IDs can be read from rich-message response blocks.
- Button rows are limited to eight controls per rich block; invalid or oversized callback data is filtered at the existing boundary.
- Existing send/edit callback semantics are preserved by the adapter layer.
- A clip can replace the snapshot message with its caption and buttons, or arrive as a silent reply when in-place media replacement is disabled.
- If Frigate has no snapshot, the text message remains the reply target for a later clip.
- Definitive Telegram rejection of a media edit can fall back to a reply. For ambiguous timeout, 429, or server errors, the implementation avoids sending a second clip that could duplicate an edit Telegram already accepted.
- Per-message/per-chat serialization prevents caption and media edits from overwriting one another while allowing independent tracked messages to proceed separately.
- Unique send operations are not retried after a potentially accepted 5xx/transport result; idempotent edit operations may retry. 429 still honors `retry_after`.

### Limitations and follow-up

- The HTML-to-rich-text adapter supports the formatting used by this app (text, line breaks, bold/italic/underline/strikethrough/code, and links). It is not a general HTML-to-Rich-Message converter; new HTML constructs need explicit mapping and tests.
- When an edit's outcome is ambiguous, the current media state intentionally drops an unconfirmed `file_id`. Future caption edits may then be skipped because using the old snapshot ID could revert media. A later reconciliation/read API would be needed to recover that ID safely.
- Retry behavior cannot make a non-idempotent Telegram send exactly-once after a lost response. The policy favors avoiding duplicate alerts; a send may be missing when Telegram accepted it but the response was lost.
- Repeated Frigate end events are covered at handler level, but durable exactly-once delivery across process restarts is not established by this change.
- Long-video handling is covered with local/mocked download and transcode paths, not a real Telegram upload at the 50 MB boundary.
- No physical Telegram client, live bot API, Home Assistant instance, or production Frigate/MQTT service was used for this review.

## Concurrency review

`tracked.lockMessage(chat)` serializes operations for one tracked Telegram message and recipient. `editCaption` and follow-up media delivery acquire it before reading current message state and hold it through the Telegram operation and state update. The global notifier mutex is not held across network calls. Tests exercise an interleaved late caption update and confirm the final caption edit uses the video `file_id`; a small lock test confirms another tracked message is not blocked by that lock.

The code still relies on in-memory tracked state. A process restart during media preparation cancels or loses the asynchronous replacement; restart recovery of a partially edited message is outside this implementation.

## Telegram API contract

The official API changelog specifies Rich Messages in Bot API 10.1 (2026-06-11), rich media blocks in 10.2 (2026-07-14), and rich button blocks in 10.3 (2026-08-24). The current button contract allows 1–8 buttons and `align` values `left`, `center`, or `right`. `sendRichMessage` accepts `rich_message`, `reply_parameters`, and `reply_markup`; `editMessageText` accepts `rich_message`. The serialized fields reviewed here follow those definitions.

References: [Telegram Bot API changelog and methods](https://core.telegram.org/bots/api), [RichMessageButton and rich button blocks](https://core.telegram.org/bots/api#richmessagebutton), [InputRichBlockButtons](https://core.telegram.org/bots/api#inputrichblockbuttons), [sendRichMessage](https://core.telegram.org/bots/api#sendrichmessage), [editMessageText](https://core.telegram.org/bots/api#editmessagetext).

## Release conclusion

Static review and local mocked tests support the implementation, but this is not a live integration sign-off. Require upstream CI and a dedicated test bot/client check before declaring the new Bot API contract production-verified. Keep this feature PR separate from the Russian localization/UI PR #48.
