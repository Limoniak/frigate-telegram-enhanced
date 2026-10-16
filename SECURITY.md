# Security policy

## Supported versions

Security fixes go to the latest release. The Docker images `:latest`, the current major
version (`:2`) and its latest minor version receive them; older versions do not.

## Reporting a vulnerability

Please do **not** open a public issue. Report it privately through GitHub:
[Report a vulnerability](https://github.com/Limoniak/frigate-telegram-enhanced/security/advisories/new)
(the *Security* tab of the repository).

Describe the problem, the affected version and, if you can, how to reproduce it. You
will get an answer within a week; once a fix is out, the advisory is published and you
are credited, unless you prefer not to be.

## Scope

The service holds a Telegram bot token, Frigate and MQTT credentials, and serves a web
interface on the local network. Of particular interest: access to the web interface or
its API without the password, leaks of the token or passwords (logs, pages, API
answers), and requests that let someone else's bot commands through.
