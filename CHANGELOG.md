# Changelog

All notable changes to this project are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `:2` image gets every 2.x release but
never a breaking 3.0, while `:latest` follows `main`.

## [Unreleased]

## [2.1.0] - 2026-10-19

### Changed

- New look for the setup and login pages: "Nocturne", dark glass on a blue night with
  a light variant that follows the system. They are built with Svelte and Tailwind
  (`web/`).
- New Nocturne interface at the address of the service: an overview (notifications over
  24 h hour by hour, connections, pause, cameras, the last notification as it arrived)
  and the notification style with a live Telegram preview on a sample photo.
- The new interface now has every screen: When, Cameras (with per-camera settings, the
  objects of each camera named with their emoji), Recent activity and Advanced settings.
- What each recipient receives is set on the When screen, under *Who receives the
  notifications?*: open a name to give that person less. Only the choices that narrow
  the global ones are offered (`#recipients` opens When); the saved settings do not
  change.
- The 24 h chart counts per clock hour in the service's time zone (12:00–13:00), the
  current hour last, instead of hours ending now; the 24 h total is the sum of its bars.
- The navigation bar shrinks once the page is scrolled.
- The footer link to the connection page reads "Telegram, Frigate and MQTT connection".
- The interface is larger on a computer screen, its cards have a single frame, and its
  faint text is more contrasted (the login card too); the one-column screens are centered. The footer says in plain words which Frigate
  notifications the service follows and its time zone.
- The save status floats at the bottom of the screen instead of shifting the page, and
  "Saved" fades after a moment; empty cards show while the interface loads.
- On a phone, the screens fold into a list under a ☰ button, and a floating button brings
  up the style's live preview.
- The 24 h chart marks 00:00, 06:00, 12:00 and 18:00 under its bars, and shows each bar's count on hover; the cameras
  of the overview show they open their settings.

### Added

- Recent activity filters (camera, outcome, hour) and lines that open on the snapshot with
  "Open in Frigate"; the bars of the 24 h chart open that hour's activity, and the same
  hours can be picked in a menu.
- Theme menu: Auto, Light or Dark (the interface, the setup page and the login page
  follow it).
- API: `GET /api/history/{id}/snapshot`; `link`,
  `has_snapshot` and `sent_at` per entry and `now` in `/api/history`. The history file
  keeps the link, the snapshot and the send time.
- Outage alerts on Telegram: Frigate not responding (`frigate/available`) or a camera
  at 0 fps in Frigate's stats for 2 minutes, then a message when it is back. Cameras
  turned off in Frigate are not reported. On by default; *Outage alerts* in the advanced
  settings (`notify.outage_alerts`) turns them off.
- Clips start 4 s before the event and end 4 s after it, as in Frigate's interface:
  a review of 2 s used to give a clip of 2 s. In `events` mode, Frigate 0.17 or later.
- When Frigate cannot give the clip, the notification gets the GIF instead of keeping
  the image alone.
- A clip over 50 MB is no longer replaced by a link: its beginning (its first 50 MB, cut
  after the last complete fragment, no re-encoding) takes the image's place, with a link
  to the whole clip in the caption. *Compress clips over 50 MB* still sends the whole
  clip re-encoded.

- A Telegram conflict (409) says whether a webhook or another program holds the bot;
  for a webhook, the overview offers *Remove the webhook*
  (`POST /api/telegram/webhook/delete`).
- API: `hour` in `/api/history`, the start of the chart's current hour.

### Removed

- The classic interface: `/classic` now leads to the new one.

### Fixed

- The number of notifications sent in the last 24 h no longer restarts at 0 after a
  restart.
- Larger step numbers on the setup page.

## [2.0.0] - 2026-10-16

### Added

- An emoji and a translated name for some 90 labels: the objects of Frigate's model and
  of Frigate+ (animals, faces, plates, bins, delivery companies…) and the common sounds
  (voices, animals, alarms, sirens, glass, gunshot…), the same in the notifications and
  the web interface. A label still unknown shows 🔊 for a sound, 🔔 for an object.
- **Sounds** in reviews mode: what Frigate's audio detection hears (barking, smoke alarm,
  speech…) is notified like an object, with the camera's image and the recording, and
  follows the same filters; the web interface offers the sounds each camera listens to.
  A sound is an alert in Frigate only when listed in `review.alerts.labels` (#4).
- German, Spanish, Italian and Portuguese, for the Telegram messages and the web
  interface. The Italian translation is by @woody4165 (#3).
- *Compress clips over 50 MB* (`notify.compress_clips`, off by default): a clip too large
  for Telegram is re-encoded with FFmpeg, now included in the image, instead of being
  sent as a link. The link remains when re-encoding fails or the clip is over 512 MB.
- A 🏠 *Home Assistant* button on notifications and in `/menu`, opening the address set in
  the web interface (*Links*) or in `home_assistant.url`. No address, no button.

### Changed

- The language switch of the web interface is a menu (🌐 FR ▾) instead of one button per
  language: with six languages, the buttons cut the title short on a phone. Its first
  choice, *Auto*, follows the browser's language, as the page does until another one is
  chosen; before, a language chosen once stayed for good.
- A clip sent as a link is now logged, with the reason.
- **Breaking:** the default mode is now `reviews`: one message per Frigate alert, so a
  person walking a dog is one notification, not two. The setup page always saved the
  chosen mode, so only a `config.yml` without `mode:` changes; add `mode: events` to keep
  one message per object, or for Frigate older than 0.14.

### Removed

- **Breaking:** environment variables are no longer read. The setup page (or
  `config.yml`) is now the only way to configure the service: `TELEGRAM_TOKEN`,
  `FRIGATE_URL`, `MQTT_BROKER` and the other variables, `LANGUAGE`, `HTTP_LISTEN`, and
  `${VAR}` in `config.yml` (now taken as is) are gone. An installation configured
  through `environment:` opens on the setup page after the update.

### Fixed

- The clip of a short event was sometimes never sent: Frigate answers HTTP 400 while
  its recording segments are not written yet, and that answer was not retried.
- A clip could last a single second: it was fetched before Frigate had stored the
  recording up to the end of the event. The clip now waits until the recording covers
  the whole event (30 s at most).

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

[Unreleased]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v2.1.0...HEAD
[2.1.0]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v1.1.0...v2.0.0
[1.1.0]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/Limoniak/frigate-telegram-enhanced/releases/tag/v1.0.0
