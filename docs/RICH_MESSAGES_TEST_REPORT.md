# Telegram Rich Messages: test report

**Date:** 2026-10-27
**Target:** `feature/telegram-rich-messages`, based on upstream `dev` at `27646bd`.

## Automated checks

| Check | Result | Evidence / boundary |
|---|---|---|
| `go test ./...` | PASS | All Go packages passed on the final code changes. Telegram requests use mocks/`httptest`. |
| `go vet ./...` | PASS | Completed with no diagnostics. |
| `git diff --check` | PASS | Final check recorded after documentation updates. |
| `go test -race ./...` | BLOCKED | Windows Go reports `-race requires cgo`; WSL and a C compiler are unavailable. Existing GitHub CI already runs race tests. |
| Docker build | NOT RUN | Docker CLI/daemon is unavailable in this environment. |
| Russian integration copy | PASS (mock) | PR #48 Russian catalog was loaded in an isolated source copy with only the new menu text and pause labels adapted; catalog parity and `/menu` text/buttons passed. No PR commits entered this feature branch. |
| Live Telegram/client | BLOCKED | No dedicated test bot/chat credentials or approved test target were discoverable. No real messages were sent. |

## Scenario matrix

PASS means a local deterministic test. It does not mean a live Telegram client test.

| ID | Scenario | Result | Evidence / boundary |
|---|---|---|---|
| T01 | Text notification | PASS (mock) | Text-only fallback and rich request serialization tests. |
| T02 | Snapshot and buttons | PASS (mock) | Notifier mock plus rich serialization; callback blocks included. |
| T03 | Snapshot → Video | PASS (mock) | `TestEndReplacesSnapshotWithClip` plus multipart `EditRichMedia` request/response assertions, including same `message_id`, caption, buttons, and returned `file_id`. |
| T04 | Snapshot → GIF | PASS (mock) | GIF follow-up path covered; live upload not run. |
| T05 | Edit rejection → fallback | PASS (mock) | Definitive rejection falls back; ambiguous timeout/5xx does not duplicate. |
| T06 | Video reply to source | PASS (mock) | Existing notifier reply test. |
| T07 | Video without snapshot | PASS (mock) | Text-only event remains reply target. |
| T08 | Event without media | PASS (mock) | Existing text-only fallback. |
| T09 | Caption after replacement | PASS (mock) | Late caption edit uses the updated media file ID. |
| T10 | Callback actions | PASS (handler mock) | Existing menu, notification, pause/resume, clip, snapshot, refresh and URL serialization tests; no live taps. |
| T11 | Russian `/menu` | PASS (integration mock) | Russian heading, line breaks, description and pause labels checked in an isolated copy of the current PR #48 catalog. |
| T12 | Long description | PASS (unit) | Caption truncation test. |
| T13 | Several events, same camera | PASS (mock) | Grouping/burst test. |
| T14 | Simultaneous independent cameras | NOT RUN | Lock isolation is tested, but no concurrent multi-camera integration run was performed. |
| T15 | Timeout/429/5xx | PASS (mock) | Retry policy, lost responses and fallback classification covered. |
| T16 | Large video | PASS (mock) | Local size/transcode paths; no real upload at the Telegram limit. |
| T17 | Repeated MQTT event | PASS (handler mock) | Duplicate end-event handling covered; no broker replay test. |
| T18 | Restart during preparation | NOT RUN | No durable in-flight recovery; tracked media state is in memory. |

## Manual evidence and limits

- Earlier user-provided screenshots show rich notifications and controls on Telegram clients. They are prior evidence, not a live test performed in this QA pass.
- Phone layout, button wrapping/colors, real snapshot-to-video rendering, real callback actions, and playback still require a dedicated test bot and client check.
- No production Telegram, Frigate, MQTT, Home Assistant, or Docker service was changed.
