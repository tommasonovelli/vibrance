# Security policy

## Supported versions

Only the latest release of Vibrance receives security fixes. Each release names the Vibrance MusicLib version it runs with ([docs/compat.md](docs/compat.md)); update the stack only with Vibrance's releases ([Upgrading](docs/operations.md#upgrading)).

Vibrance 0.1.0 is built with Go 1.25.14, the toolchain Vibrance MusicLib 1.2.0 is built with. It is the last release of the Go 1.25 series, which the Go project no longer supports: a vulnerability found in Go's standard library from now on is fixed only in newer series. Vibrance stays on MusicLib's pinned versions for this release and will move to a supported Go in a later one. Before the release, `govulncheck` found no known vulnerability that Vibrance's code reaches; if one is found later, it is fixed by a new release of Vibrance built with a newer Go.

## Reporting a vulnerability

**Do not open a public issue, and do not publish details, for a vulnerability.** Report it privately to:

> **SECURITY CONTACT: TO BE SET BY THE OWNER** (a private e-mail address, or GitHub's "Report a vulnerability" button under the repository's **Security** tab once private vulnerability reporting is enabled). Until this line is replaced there is no private channel: open an issue that only says you have a security report, with no details, and wait to be contacted.

Please include:

- the version (`docker compose run --rm --no-deps vibrance version`) and how Vibrance is reached (directly, through Caddy or another proxy);
- what an attacker can do, and what they need for it (no account, a user account, the local network);
- the steps to reproduce it, as requests or commands;
- the relevant log lines.

**Never send** your `.env`, passwords, session cookies, tokens, a copy of `vibrance.db` (it holds the password hashes) or private music. Vibrance's log never holds a password, a token or a cookie: its lines are safe to quote.

## What to expect

Vibrance is maintained by one person, without a guaranteed response time. A confirmed vulnerability is fixed in a new release, and the release notes say what was fixed and who reported it, unless you prefer not to be named.

## What Vibrance protects, and what it does not

Vibrance is made for a home network, or for a private name behind HTTPS ([Access from other devices](docs/operations.md#access-from-other-devices)). It is not made to be exposed directly to the Internet.

- It reads MusicLib's library and never writes to it: its container mounts that volume read-only, with a read-only root filesystem, no capabilities and a user that is not root.
- Every request needs an account, except the health checks, `GET /api/v1/server`, the sign-in and the documentation of the API. A user sees only their own favorites, playlists and sessions; the accounts and the state of the library are for admins.
- Passwords are stored as argon2id hashes and session tokens as SHA-256; neither is ever logged.
- It answers only requests addressed to its public origin (`VIBRANCE_PUBLIC_ORIGIN`), refuses a foreign `Origin`, asks for the header `X-Vibrance-Request: 1` on every request that changes something, and sends no CORS header.
- **Over plain HTTP, passwords and session cookies cross the network in clear.** Use HTTPS, or an SSH tunnel, on any network you do not trust.
- There is no limit on sign-in attempts per address, only a delay: one password is checked at a time, and a refusal takes a second. Use long random passwords for every account.
- Whoever can run `docker` on the machine, or read `.env` or the volumes, controls Vibrance: the admin password of the first start is in `.env`, and `vibrance user reset-password` needs no password.

Third-party software in the image is listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md); report a vulnerability of FFmpeg, Go or another component to its own project too.
