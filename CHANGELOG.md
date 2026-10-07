# Changelog

All notable changes to Vibrance are recorded in this file, in the format of [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Every track in one list.** `GET /api/v1/tracks` lists the available tracks, paged, by title, by artist, album after album, or by when Vibrance first saw them, in either direction, optionally only those of the albums of one artist. A track that MusicLib moves to another album keeps its place among the recently added. The first start after the upgrade computes the sort keys of every track once before it serves.
- **Covers of the playlists.** Every playlist the API returns carries `covers`: up to four covers of distinct albums from its available items, in their order, for a mosaic.
- **How much there is.** `GET /api/v1/catalog/summary` counts the available artists, albums and tracks and their duration; `GET /api/v1/me/favorites/summary` counts the favorites of the user and the duration of the available ones.
- **Shuffle.** `GET /api/v1/tracks/random` chooses up to 200 available tracks at random, none twice in one answer, from the whole library or from the albums of one artist.
- **Several favorites at once.** `POST /api/v1/me/favorites/tracks` makes a list of tracks favorites in one request, all or none, and the list of the favorites shows them in the order sent.
- **Sign out everywhere else.** `DELETE /api/v1/me/sessions` ends every session of the account, cookies and tokens, except the one of the request.
- **The playlists that hold a track.** `GET /api/v1/tracks/{id}/playlists` lists the playlists of the user that have the track. A new migration adds the index it reads.
- **The same preferences on every device.** `GET /api/v1/me/settings` answers the preferences of the user: volume leveling (`automatic` or `off`), single-key shortcuts and the theme (`dark` or `light`), with the defaults `automatic`, `false` and `dark` for a user who never saved any. `PATCH /api/v1/me/settings` changes only the fields it sends and answers every preference. They are deleted with the account. A new migration adds their table.
- **The web interface.** The server now carries a listening interface and serves it at its address: `/` opens the library, `/login` the sign-in page, and every other path outside `/api` and `/health` the page of the interface. It is made of the files in the binary alone, with a `Content-Security-Policy` that lets the pages load only from the server, and loads nothing from another host. It embeds the Hanken Grotesk font (SIL Open Font License 1.1).

### Changed

- `/` serves the web interface instead of redirecting to `/api/docs`. The documentation stays at `/api/docs`.

## [0.1.0] - 2026-10-06

The first release. Vibrance runs next to Vibrance MusicLib 1.2.0 in one Docker Compose stack, reads the library MusicLib writes, read-only, and serves it to listen to. To install it, or to add it to an existing MusicLib installation, follow the [operations guide](https://github.com/tommasonovelli/vibrance/blob/v0.1.0/docs/operations.md).

### Added

- **The library, kept up to date by itself.** A scanner indexes MusicLib's library in the background, at the start, every 5 minutes (`VIBRANCE_SCAN_INTERVAL`) and on an admin's request. It reads the tags with MusicLib's own `ffprobe` and never writes to MusicLib's volume. Albums and tracks that leave the library are shown as unavailable, never deleted, and come back with the same ids.
- **Tracks keep their identity.** A track is recognised by its audio fingerprint, not by its file name or its number, so renaming an album, retagging a track or renumbering it keeps its id, its place in playlists and its favorite. When MusicLib moves a track to another album, playlists and favorites follow it.
- **Accounts.** An admin and users, created by an admin (no self sign-up); the first admin comes from `.env`. Browsers sign in with a cookie, apps with a bearer token. A browser stays signed in for as long as it is used, and signs in again after 30 days without a request; a token, after 90. Passwords are stored with argon2id; a change of password signs out the account's other sessions, and a reset or disabling the account signs it out everywhere at once.
- **The catalog**: artists, albums and tracks, sorted and paged, with album covers (the original or a thumbnail of 256 or 640 pixels; very large covers are turned into thumbnails one at a time, to bound the memory this takes) and the lyrics of the `.lrc` files as lines: synchronised, with their times, or plain, with an empty line between the stanzas.
- **Streaming of the original files**, with ranges and caching, without any transcoding.
- **Search** of artists, albums and tracks as you type, insensitive to case and accents. A letter and its accent sent as two characters, as some systems write them, find the same names as the single character, in every script (the query is read in Unicode normalization form C, the form of the names in the library).
- **Favorites and private playlists** for each user; a playlist keeps up to 10,000 items, and changes that depend on positions are protected against concurrent edits: every answer that carries one playlist has its entity tag in the `ETag` header and in the body.
- **The HTTP API** under `/api/v1`, described by an OpenAPI 3.0.3 specification that the server serves at `/api/openapi.yaml` and shows, with a page to try every request, at `/api/docs`. Every request that changes something carries the header `X-Vibrance-Request: 1`; the specification declares it, so generated clients and the page send it by themselves.
- **Operations**: `vibrance backup`, `restore` and `doctor` for Vibrance's database, and `vibrance rebuild-search`, which makes the search index again from the library index when `doctor` finds it damaged or out of step; the Compose stack (MusicLib's own `compose.yaml` plus the `vibrance` service), optional HTTPS with Caddy and two names, and the operations guide. A stop gives the tracks that are playing 10 seconds, and the container 15 (`stop_grace_period`), so that Vibrance closes its database itself; a Vibrance that is killed recovers at its next start all the same.

### Known limits

- There is no web player yet: Vibrance 0.1.0 is a server with an API and its documentation page.
- Search does not segment Chinese or Japanese text, and has no tolerance for typing mistakes.
- In a lyrics file a time tag of three numbers without a point, `[mm:ss:xx]`, is read as minutes, seconds and fraction: a file that writes hours without a fraction, `[hh:mm:ss]`, gets wrong times.
- Only Linux amd64 with Docker Engine and local ext4 storage is supported, as for MusicLib.
- This release was tested only on Docker Desktop for Windows, not yet on a native Linux Docker Engine, which is the platform it supports: there the quality gate, the contract suite with the real MusicLib 1.2.0 (twice in a row), the stack smoke test, `govulncheck` and the image build all passed.
- Vibrance 0.1.0 is built with Go 1.25.14, to stay on the same pinned toolchain as MusicLib 1.2.0. It is the last release of the Go 1.25 series, which the Go project no longer supports. No known vulnerability reaches Vibrance's code at the time of the release (`govulncheck`); a later release moves to a supported Go.

[Unreleased]: https://github.com/tommasonovelli/vibrance/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/tommasonovelli/vibrance/releases/tag/v0.1.0
