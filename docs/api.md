# Using the API

The reference is the OpenAPI document the server itself serves: `GET /api/openapi.yaml`, and `GET /api/docs` for a page that shows it and can send requests (`/` leads there). Neither needs a session, and the page loads nothing from another host. This file is a short tour with `curl`.

The examples assume a server at `http://127.0.0.1:8090` (its `VIBRANCE_PUBLIC_ORIGIN`), an account `anna` whose password is in the shell variable `VIBRANCE_PASSWORD`, and a POSIX shell.

```sh
BASE=http://127.0.0.1:8090/api/v1
```

Three rules hold for every request (the specification has the others):

- The request must be addressed to the public origin: another `Host` answers `421 host_not_allowed`.
- Every request other than GET and HEAD carries `X-Vibrance-Request: 1`, signing in included: without it the answer is `403 request_header_required`.
- Errors are JSON, `{"code": ..., "message": ..., "details": {...}}`. Test the `code`, which is stable.

## Signing in with a cookie

A browser signs in with `POST /auth/login` and keeps the cookie `vibrance_session`. With `curl`, a cookie jar does the same:

```sh
curl -sS -c cookies.txt -H 'X-Vibrance-Request: 1' -H 'Content-Type: application/json' \
  -d "{\"username\":\"anna\",\"password\":\"$VIBRANCE_PASSWORD\"}" "$BASE/auth/login"
curl -sS -b cookies.txt "$BASE/me"
curl -sS -b cookies.txt -H 'X-Vibrance-Request: 1' -X POST "$BASE/auth/logout"
```

A wrong name or password answers `401 invalid_credentials`, after about a second.

## Signing in with a token

A client that is not a browser asks for a token, once, and sends it as a bearer token. The token is shown only in this answer; the server keeps only its hash. `device_name` is what the list of sessions (`GET /me/sessions`) shows.

```sh
TOKEN=$(curl -sS -H 'X-Vibrance-Request: 1' -H 'Content-Type: application/json' \
  -d "{\"username\":\"anna\",\"password\":\"$VIBRANCE_PASSWORD\",\"device_name\":\"curl\"}" "$BASE/auth/tokens" |
  sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
AUTH="Authorization: Bearer $TOKEN"
curl -sS -H "$AUTH" "$BASE/me"
```

The examples below use the token. `POST /auth/logout` with it ends its session.

## Albums

Lists are paginated with `limit` (default 50, at most 200) and `after`: send the `next` of a page as the `after` of the following request, with the same other parameters, until `next` is `null`.

```sh
curl -sS -H "$AUTH" "$BASE/albums?sort=added&order=desc&limit=20"
curl -sS -H "$AUTH" "$BASE/albums?sort=added&order=desc&limit=20&after=CURSOR"   # CURSOR: the "next" of the page before
curl -sS -H "$AUTH" "$BASE/albums/ALBUM_ID"                                      # the album with its tracks
curl -sS -H "$AUTH" "$BASE/search?q=kind+of+blue"
```

The cover of an album is at the `cover.url` of the album (`/api/v1/albums/ALBUM_ID/cover?v=HASH`); add `size=256` or `size=640` for a thumbnail.

## Audio, with `Range`

The audio of a track is its file, as MusicLib wrote it. Ranges and conditional requests work as for any file; the `ETag` is the SHA-256 of the file.

```sh
curl -sS -H "$AUTH" -o track.flac "$BASE/tracks/TRACK_ID/audio"                 # the whole file: 200
curl -sS -H "$AUTH" -r 0-65535 -o head.bin -D - "$BASE/tracks/TRACK_ID/audio"   # the first 64 KiB: 206, with Content-Range
curl -sS -H "$AUTH" -r 65536- -H 'If-Range: "ETAG"' -o rest.bin "$BASE/tracks/TRACK_ID/audio"
```

With `If-Range`, the range is served only while the file is still the one with that `ETag`; if MusicLib has replaced it, the answer is the whole new file (`200`), never bytes of two files. `503 library_changing` with `Retry-After` means the file has just changed and the scanner is looking at its album: ask again after that many seconds. `404 track_unavailable` means the file is no longer in the library.

In a web page of the same origin, `<audio src="/api/v1/tracks/TRACK_ID/audio">` works with the session cookie alone.

## A playlist, with `If-Match`

Every change gives a playlist a new `revision` and a new `etag`, `"playlist:<id>:<revision>"`, quotes included. Adding at the end needs no precondition; a change that depends on positions (inserting at a position, moving an item) needs `If-Match` with the current `etag`.

```sh
# Create it: 201, with the playlist and its etag.
curl -sS -H "$AUTH" -H 'X-Vibrance-Request: 1' -H 'Content-Type: application/json' \
  -d '{"name":"Evening","description":""}' "$BASE/playlists"

# Add two tracks at the end: no If-Match needed. The answer has the new etag and the ids of the items.
curl -sS -H "$AUTH" -H 'X-Vibrance-Request: 1' -H 'Content-Type: application/json' \
  -d '{"track_ids":["TRACK_ID_1","TRACK_ID_2"],"position":null}' "$BASE/playlists/PLAYLIST_ID/items"

# Move the second item to the top: If-Match is required.
curl -sS -H "$AUTH" -H 'X-Vibrance-Request: 1' -H 'Content-Type: application/json' \
  -H 'If-Match: "playlist:PLAYLIST_ID:2"' \
  -d '{"position":0}' "$BASE/playlists/PLAYLIST_ID/items/ITEM_ID/move"

curl -sS -H "$AUTH" "$BASE/playlists/PLAYLIST_ID/items"
```

Without `If-Match` the move answers `428 precondition_required`. With the `etag` of an older revision it answers `412 precondition_failed` and changes nothing: read the playlist again (`GET /playlists/PLAYLIST_ID`), apply the change to what it is now, and send it with the new `etag`.

## The state of the library (admins)

```sh
curl -sS -H "$AUTH" "$BASE/admin/library"                                       # what the scanner is doing, and its problems
curl -sS -H "$AUTH" -H 'X-Vibrance-Request: 1' -X POST "$BASE/admin/library/scan"   # ask for a scan: 202, at once
```

A scan is always safe to ask for: it deletes nothing, and requests made while one runs become a single following cycle.
