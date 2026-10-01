# Changelog

All notable changes to this project are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the provided `docker-compose.yml` uses the
`:1` image, which gets every 1.x release but never a breaking 2.0.

## [Unreleased]

## [1.0.0] - 2026-10-01

First release.

### Notifications

- **events** mode (one message per detected object) or **reviews** mode (Frigate ≥ 0.14 alerts).
- Instant snapshot, then the MP4 clip in the same message (or as a reply), optional GIF; above 50 MB, a link to the clip.
- Frigate's GenAI description and labels (custom classification, faces, plates) added to the caption as soon as they are known.
- Filters per camera, object, zone, minimum score, severity and label; cooldown; quiet hours and off hours; burst grouping; cropped snapshots.
- Several recipients with per-camera routing and per-recipient restrictions; presence from Home Assistant or any MQTT topic.
- After an MQTT outage, detections missed during the last hour are caught up from Frigate.
- A send that reached Telegram is never repeated, so a lost response no longer duplicates a notification.

### Telegram

- Buttons on each notification (live image, clip, mute camera 1 h, pause 30 min), `/menu` control panel, and `/pause`, `/resume`, `/status`, `/cameras`, `/snapshot`, `/last`.
- An unauthorized user who writes to the bot in private gets their Telegram ID.

### Web interface

- Setup from the browser: bot token (the chats that send `/start` are offered), Frigate found on the network, its MQTT broker read from Frigate's configuration. Nothing to fill in `docker-compose.yml`.
- Notification settings saved by themselves and applied without a restart; per-camera overrides; test notification.
- Recent activity (kept across restarts) with the reason for every ignored detection; connection status with what to fix; Telegram users recently refused.
- Optional password with a login page and a 30-day session.
- English and French; other languages can be added with a single catalog file.

### Operations

- Persistent state, `/healthz`, Prometheus metrics (optionally password-protected).
- Multi-arch distroless image (amd64, arm64) running as non-root, published as `:1`, `:1.0`, `:1.0.0`, `:latest` (main) and `:dev`.

[Unreleased]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/Limoniak/frigate-telegram-enhanced/releases/tag/v1.0.0
