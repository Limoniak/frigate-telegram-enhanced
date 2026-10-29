# Changelog

All notable changes to this project are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `:2` image gets every 2.x release but
never a breaking 3.0, while `:latest` follows `main`.

## [Unreleased]

### Added

- Russian localization for the web interface, setup flow, errors and Telegram messages,
  including Russian date and time formatting. Contributed by @tribul8ion (#48).

### Changed

- Telegram pause-menu buttons now use translated labels.
- Native select option menus follow the selected light or dark theme more closely.

### Security

- Built with Go 1.27.3, which distrusts compromised intermediate certificates in
  `crypto/x509` (GO-2026-6809).

## [2.2.2] - 2026-10-20

### Added

- *Show in the caption* (Style screen): a switch each for the zones, the score, the
  *Open in Frigate* link and the date (without it, the time alone). Saved as
  `show_zones`, `show_score`, `show_link` and `show_date`, on by default; each camera can
  set its own under *All settings*.
- An emoji per camera (Cameras screen, `emoji`), written before its name in the
  notifications: 🚪 front door. A picker offers those a camera's place suggests, and finds
  any other by its name in the interface's language (names from Emojibase).
- In reviews mode, the caption shows the score of the review's first detection, read
  from Frigate before the notification leaves (3 s at most); the recent activity keeps
  it too.
- Slovenian, for the Telegram messages and the web interface.

## [2.2.1] - 2026-10-20

### Fixed

- The sites listed under *Sites that may show this page* were dropped by the next
  change made on any screen after the page was opened again, and the dashboards showing
  the interface in a frame went blank.
- A tab left open (on a phone, say) reads the settings again when it is shown, so that
  its next change no longer brings back the settings another tab replaced since.
- With *Compress clips over 50 MB*, a large clip on a slow link is no longer cut after
  2 minutes and downloaded again three times: only the wait for Frigate to start
  sending it is bounded, and a transfer that stops still ends after 15 s.

## [2.2.0] - 2026-10-20

### Changed

- *What should be reported?* offers the objects and sounds the cameras track in Frigate,
  each with its emoji (none ticked = everything), instead of people / people and cars /
  everything; the same on each camera and for each recipient.
- The night of the When screen can be set (22:00–07:00 by default): the night choices,
  and the recipients' ones, follow it. It is saved as `night` with the settings.
- In English, times follow the browser's variant: a 12-hour clock in American English,
  24-hour otherwise.
- On the connection page, *More options* is a card of its own (it was folded under the
  interface's password), and the time zone is a list one searches by typing.
- Resetting a camera's settings asks first, and every reset asks in a dialog in the
  interface's look instead of the browser's.
- The previews write the time as the Telegram messages do; the last notification of the
  overview no longer shows the sample's GenAI description or clip length.
- The Cameras screen lists the cameras in Frigate's order, as the overview does; a camera
  turned off dims its name rather than its whole card.
- Every screen has the same heading, and the navigation bar the service's mark.
- The bot writes the end of a pause or a mute, and the start of an outage, with the
  time alone when it is today, and the date otherwise.
- `/resume`, `/last` and `/snapshot` name a camera Frigate doesn't know, with the list
  of cameras, as `/pause` does; their errors are translated.
- The 🎬 Clip button is translated.
- Every page, in both themes, passes axe-core's WCAG 2.1 A and AA checks, run by the
  browser tests on each change; the connection page's time zone list now has a name
  for screen readers.
- Another site can no longer show the interface in a frame (clickjacking): only Home
  Assistant at the address of its button, and the addresses listed under *Sites that
  may show this page* (Advanced settings, Links), for a dashboard's Webpage card. Every
  response also carries `nosniff` and a same-origin referrer policy.
- The French texts all say *vous*, in the interface and the README; a test keeps French
  and German to one register. Italian uses the same ellipsis and quotes as elsewhere.
- While the overview loads its count, the card shows placeholders instead of staying
  empty.
- A new mark, *Ouverture* (an open lens and a dot leaving it), replaces the bell: in the
  favicon, which follows the browser's theme, in the navigation bar, on the connection
  page and at the top of the README. The README tells how to give the bot the matching
  picture (`docs/images/bot-avatar.png`), which the phone of the previews shows too.
- The phone of the previews follows the interface's theme: Telegram's night mode in the
  dark theme, its day mode in the light one; the phone itself is black on the night and
  white on the light page.
- The connection to Telegram, Frigate and MQTT is the last item of the menu,
  *Connection*, instead of a link at the bottom of the page.
- The minimum score per object names the objects as the other screens do.

### Fixed

- Five wrong passwords from an address no longer lock out the people already logged in
  from it (everyone shares one address behind a reverse proxy or a home router).
- A time range that starts where it ends (e.g. 00:00–00:00) is refused instead of
  never applying.
- A detection waiting for its label is no longer lost when the event queue is full.
- An object Frigate recognized (a known face, plate or class of its own), which
  Frigate 0.16 calls `person-verified` or `car-verified` in its reviews, is its object
  again: it was shown as "🔔 car-verified" and, with an object filter, not notified at
  all.
- On a phone, the live preview button of the Style screen is a round icon that no longer
  hides the burst choices, and those choices wrap instead of squeezing.
- In the light theme, a switch turned off is visible; switches, choices and the login
  page's languages are easier to tap.
- A connection that only needs attention is shown in amber, not red.
- With a filter, the footer of the Recent activity screen counts what it shows.
- The fields given a width of their own (hours, durations, scores, the "add…" fields) no
  longer stretch across the whole card.
- In the advanced settings, the objects and the severities are named as on the other
  screens (🚗 Car, Alerts) instead of Frigate's raw names.
- On the connection page, the choices of *What triggers a notification* fit a phone's
  screen: the details moved to a hint below.
- A night, or a quiet or off hours range, that starts where it ends is flagged on its
  field and not sent, instead of being silently ignored (the night) or refused by the
  server, which then refused every later change until it was fixed (the ranges).

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

[Unreleased]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v2.2.2...HEAD
[2.2.2]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v2.2.1...v2.2.2
[2.2.1]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v2.2.0...v2.2.1
[2.2.0]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v2.1.0...v2.2.0
[2.1.0]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v1.1.0...v2.0.0
[1.1.0]: https://github.com/Limoniak/frigate-telegram-enhanced/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/Limoniak/frigate-telegram-enhanced/releases/tag/v1.0.0
