# Telegram Rich Messages: UI QA

**Date:** 2026-10-27

## Issues found and changes

| Area | Finding | Change / result |
|---|---|---|
| `/menu` | Camera description appeared without a section heading, making the status and camera controls run together visually. | Added a localized Cameras heading and a short description on separate lines. A bot mock checks the exact section boundary. |
| `/snapshot` | Choosing a camera sent the image as a reply to “Pick a camera”, producing a large Telegram quote block. | Added a distinct picker callback that sends a standalone image. Notification “Now” keeps the established reply to its source event. Mock tests cover both paths. |
| Pause labels | `/menu` used English labels directly, despite localization keys being the intended shared mechanism. | Menu now translates 30 min, 1 h and 8 h labels. All six existing language catalogs include the keys. Russian rendering passed against a temporary integration copy of PR #48's catalog. |
| Notification media | UI screenshots showed rich photo/video and grouped buttons; phone button colors and widths varied by client. | No size/color hacks added. The app keeps documented left alignment and logical button rows; client-side appearance remains Telegram-controlled. |

## `/snapshot`, `/last`, and notification captions

- `/snapshot` without a camera presents the existing camera selector. A selection now sends a standalone live image with camera name and current time. `/snapshot <camera>` already sends a standalone image.
- The notification “Now” button still replies to its event message, retaining its event context.
- `/last` uses the shared event caption builder, preserving the event/camera label, available confidence/time, and Frigate link. It does not invent fields absent from an event.
- Video/GIF replacement retains the event caption and callback controls in the same rich message. Separate fallback media continues to use the existing reply/context behavior.

## API and client behavior

The Bot API exposes row alignment and button style fields, but not fixed button widths or CSS-like color control. Clients render button dimensions, line wrapping, and theme colors. Telegram documents reply-target fallback and optional quote content; these do not provide a general way to hide a normal reply's quote. The picker now avoids creating that reply relationship instead of trying unsupported parameters.

References: [ReplyParameters](https://core.telegram.org/bots/api#replyparameters), [RichMessageButton](https://core.telegram.org/bots/api#richmessagebutton), [InputRichBlockButtons](https://core.telegram.org/bots/api#inputrichblockbuttons).

## Verification

- PASS: menu line-break mock, action callbacks, picker standalone image, legacy notification reply, `/last` existing tests, and rich-media multipart/edit serialization tests.
- PASS (mock integration): Russian menu strings and pause labels with a temporary PR #48 catalog copy.
- PASS: full Go test suite and `go vet`.
- BLOCKED: race detector on this Windows host because CGO is unavailable; no WSL installation or C compiler was found. Existing Linux CI runs race tests.
- NOT RUN: Docker build, live Bot API test, and physical phone/client screenshots in this QA pass. No dedicated test credentials/chat were identified, and no production chat was contacted.

## Manual confirmation still needed

Use a dedicated test bot and chat to confirm on the target Telegram clients:

1. `/menu` headings and button row grouping in light and dark themes.
2. `/snapshot` selection sends a clean standalone photo without the picker quote.
3. A Frigate event snapshot becomes a video in the same message and keeps caption/buttons.
4. Pause/resume, snapshot, clip, camera toggle, refresh, Frigate URL, and Home Assistant URL actions.

Previous user-provided screenshots document earlier real-client behavior, but are not evidence that these latest changes were live-tested.
