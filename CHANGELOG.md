# Changelog

All notable changes to this project are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `:1` image gets every 1.x release but
never a breaking 2.0, while `:latest` follows `main`.

## [Unreleased]

### Added

- *Compress clips over 50 MB* (`notify.compress_clips`, off by default): a clip too large
  for Telegram is re-encoded with FFmpeg, now included in the image, instead of being
  sent as a link. The link remains when re-encoding fails or the clip is over 512 MB.
- A 🏠 *Home Assistant* button on notifications and in `/menu`, opening the address set in
  the web interface (*Links*) or in `home_assistant.url`. No address, no button.

### Changed

- A clip sent as a link is now logged, with the reason.

### Removed

- **Breaking:** environment variables are no longer read. The setup page (or
  `config.yml`) is now the only way to configure the service: `TELEGRAM_TOKEN`,
  `FRIGATE_URL`, `MQTT_BROKER` and the other variables, `LANGUAGE`, `HTTP_LISTEN`, and
  `${VAR}` in `config.yml` (now taken as is) are gone. An installation configured
  through `environment:` opens on the setup page after the update.

### Fixed

- The clip of a short event was sometimes never sent: Frigate answers HTTP 400 while
  its recording segments are not written yet, and that answer was not retried.

## [1.1.0] - 2026-10-01

### Added

- The setup page checks the MQTT broker with a *Check* button, without saving anything.
- A saved password (Frigate, MQTT, web interface) can be removed from the *Connection* page.

### Changed

- Admins are no longer required. Without them, anyone in a recipient chat controls the
  bot: the person of a private chat, the members of a group. Listed admins stay the only ones.
- Adding a recipient by ID on the setup page: an invalid ID is explained, Enter adds it,
  spaces are accepted, and the field opens by itself while nobody was found.
- The message to a refused Telegram user, and the refused users box of the interface,
  point to the *Connection* page when the service was set up from the browser.

### Fixed

- **Security**: a saved Frigate or MQTT password is only reused for the address it was
  saved for. Checking another address from the *Connection* page could send it there.
- A button whose message Telegram no longer sends (an old one) answered "Not allowed"
  to an admin instead of working.

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

[Unreleased]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/Limoniak/frigate-telegram-enhanced/releases/tag/v1.0.0
