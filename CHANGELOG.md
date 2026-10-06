# Changelog

All notable changes to Vibrance are recorded in this file, in the format of [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-05

The first release. Vibrance runs next to Vibrance MusicLib 1.2.0 in one Docker Compose stack, reads the library MusicLib writes, read-only, and serves it to listen to. To install it, or to add it to an existing MusicLib installation, follow the [operations guide](https://github.com/tommasonovelli/vibrance/blob/v0.1.0/docs/operations.md).

### Added

- **The library, kept up to date by itself.** A scanner indexes MusicLib's library in the background, at the start, every 5 minutes (`VIBRANCE_SCAN_INTERVAL`) and on an admin's request. It reads the tags with MusicLib's own `ffprobe` and never writes to MusicLib's volume. Albums and tracks that leave the library are shown as unavailable, never deleted, and come back with the same ids.
- **Tracks keep their identity.** A track is recognised by its audio fingerprint, not by its file name or its number, so renaming an album, retagging a track or renumbering it keeps its id, its place in playlists and its favorite. When MusicLib moves a track to another album, playlists and favorites follow it.
- **Accounts.** An admin and users, created by an admin (no self sign-up); the first admin comes from `.env`. Browsers sign in with a cookie, apps with a bearer token. A browser stays signed in for as long as it is used, and signs in again after 30 days without a request; a token, after 90. Passwords are stored with argon2id; a change of password signs out the account's other sessions, and a reset or disabling the account signs it out everywhere at once.
- **The catalog**: artists, albums and tracks, sorted and paged, with album covers (the original or a thumbnail of 256 or 640 pixels) and the lyrics of the `.lrc` files as lines: synchronised, with their times, or plain, with an empty line between the stanzas.
- **Streaming of the original files**, with ranges and caching, without any transcoding.
- **Search** of artists, albums and tracks as you type, insensitive to case and accents.
- **Favorites and private playlists** for each user; a playlist keeps up to 10,000 items, and changes that depend on positions are protected against concurrent edits: every answer that carries one playlist has its entity tag in the `ETag` header and in the body.
- **The HTTP API** under `/api/v1`, described by an OpenAPI 3.0.3 specification that the server serves at `/api/openapi.yaml` and shows, with a page to try every request, at `/api/docs`. Every request that changes something carries the header `X-Vibrance-Request: 1`; the specification declares it, so generated clients and the page send it by themselves.
- **Operations**: `vibrance backup`, `restore` and `doctor` for Vibrance's database; the Compose stack (MusicLib's own `compose.yaml` plus the `vibrance` service), optional HTTPS with Caddy and two names, and the operations guide.

### Known limits

- There is no web player yet: Vibrance 0.1.0 is a server with an API and its documentation page.
- Search does not segment Chinese or Japanese text, and has no tolerance for typing mistakes.
- In a lyrics file a time tag of three numbers without a point, `[mm:ss:xx]`, is read as minutes, seconds and fraction: a file that writes hours without a fraction, `[hh:mm:ss]`, gets wrong times.
- Only Linux amd64 with Docker Engine and local ext4 storage is supported, as for MusicLib.

[Unreleased]: https://github.com/tommasonovelli/vibrance/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/tommasonovelli/vibrance/releases/tag/v0.1.0
