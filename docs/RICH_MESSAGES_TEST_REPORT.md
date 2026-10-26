# Telegram Rich Messages: test report

**Date:** 2026-10-27
**Test target:** `feature/telegram-rich-messages`, based on upstream `dev` at `27646bd`.

## Automated checks

| Command | Result | Notes |
|---|---|---|
| `go test ./...` | PASS | All Go packages passed. Tests use local fakes and `httptest`; no real Telegram calls. |
| `go vet ./...` | PASS | No diagnostics. |
| `git diff --check` | PASS | No whitespace errors in the reviewed changes. |
| `go test -race ./...` | BLOCKED | This Windows environment has `CGO_ENABLED=0` and no C compiler; Go requires cgo for `-race`. |
| `go test -tags browser -run Browser ./internal/web/` | NOT RUN | Not part of this Go-only feature change; no browser validation performed. |
| Full GitHub CI / Docker build | NOT RUN | Requires upstream CI; no image was built or deployed. |

`go test -list '^Test' ./...` found 299 top-level test functions across the repository. This is a discovered test count, not an assertion that all 299 relate to Rich Messages. The final `go test ./...` run passed.

## Scenario matrix

“PASS (mock)” means a deterministic test exercises the application path with fake Frigate/Telegram or an `httptest` Bot API server. It does not mean a live bot or mobile client was tested.

| ID | Scenario | Result | Evidence / boundary |
|---|---|---|---|
| T01 | Text notification | PASS (mock) | `TestMissingSnapshotFallsBackToText`; Telegram client serialization tests. |
| T02 | Snapshot and buttons | PASS (mock) | `TestNewEventSendsSnapshotToEveryChat`; rich media and button serialization tests. |
| T03 | Snapshot replaced by video | PASS (mock) | `TestEndReplacesSnapshotWithClip`, `TestEditRichMediaUsesRichMessage`. |
| T04 | Snapshot followed by GIF | PASS (mock) | `TestGIFWhenTheClipIsUnavailable`; no Telegram client upload. |
| T05 | Edit rejection and fallback | PASS (mock) | `TestClipReplacementFallbackOnlyAfterDefinitiveRejection`: 400, “not modified”, timeout, and 502 outcomes. |
| T06 | Video reply to original snapshot | PASS (mock) | `TestEndSendsClipAsReplyToSnapshot`. |
| T07 | Video without snapshot message | PASS (mock) | `TestClipRepliesToTextOnlyNotification`. |
| T08 | Event without media | PASS (mock) | Text-only snapshot fallback path; live Frigate not used. |
| T09 | Caption update after media replacement | PASS (mock) | `TestLateCaptionUpdateDoesNotOverwriteClipReplacement`. |
| T10 | Callback actions | PASS (handler mock) | `TestCallbacks`, `TestMenu`, snapshot callback tests; no live button taps. |
| T11 | Russian `/menu` | BLOCKED | The isolated branch intentionally excludes PR #48 and therefore has no Russian catalog. Recheck after that PR merges or against a separately prepared integration branch. |
| T12 | Long event description | PASS (unit) | `TestBuildCaptionTruncatesDescriptionAtRichMessageLimit`. |
| T13 | Several events for one camera | PASS (mock) | `TestBurstIsGroupedIntoTheFirstMessage`. |
| T14 | Simultaneous independent cameras | NOT RUN | Per-message lock isolation is unit-tested; no concurrent multi-camera integration run. |
| T15 | Timeout, 429, and 5xx | PASS (mock) | `TestRetryOn429`, `TestRetryOn5xx`, `TestUniqueSendNotRetriedOn5xx`, response-lost tests, and T05. |
| T16 | Large video | PASS (mock) | Oversize/truncation/compression paths are tested locally; actual 50 MB Telegram upload is NOT RUN. |
| T17 | Repeated MQTT event | PASS (handler mock) | Duplicate end delivery is exercised in `TestEndReplacesSnapshotWithClip`; no MQTT broker replay test. |
| T18 | Restart during media preparation | NOT RUN | No process restart or durable in-flight media recovery test. |

## Manual/integration coverage

- No real Telegram bot, chat, phone, Bot API server, or production service was contacted from these tests.
- Local test runs establish request bodies, retries, and notifier decisions against mocks only.
- Mobile layout, button wrapping, camera/clip playback, and client-specific Rich Message rendering still need a dedicated test bot check.
- Race-detector validation remains outstanding because cgo is unavailable in this environment.
