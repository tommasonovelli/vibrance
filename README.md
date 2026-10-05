# Vibrance

Vibrance is the listening side of [Vibrance MusicLib](https://github.com/tommasonovelli/vibrance-musiclib). It is a read-only server over the `library/` folder that MusicLib produces, with multiple users, favorites, playlists, full-text search and an HTTP API documented with OpenAPI. The two products share only that folder.

**Status:** in development toward v0.1. So far the server starts, creates and migrates its SQLite database, checks its `ffmpeg` and `ffprobe`, keeps an index of the MusicLib library up to date in the background, answers its health endpoints and stops. Its API is specified in `api/openapi.yaml`, and the server does not serve it yet.

## Trying it

Everything is built, tested and run in Docker. The host needs only Docker Engine or Docker Desktop with the Compose v2 plugin: no Go and no FFmpeg. On Windows, run the scripts from Git Bash.

```sh
scripts/check.sh                 # the gate: generated code up to date, go build, go vet, gofmt, go test -race (no network)
scripts/check.sh ./internal/media/...   # the gate on one package tree
scripts/dev.sh                   # a shell in the toolchain container, on the live sources
scripts/dev.sh go test -race -count=20 -run TestX ./internal/media
scripts/lint-shell.sh            # shellcheck on every shell script
scripts/fuzz.sh FuzzParseReceipt 60s ./internal/library   # one fuzz target, for a duration, on the live sources
scripts/sqlc.sh                  # regenerate internal/store from sql/ and the migrations (sqlc.sh diff: only compare)
scripts/generate.sh              # regenerate internal/api from api/openapi.yaml (generate.sh diff: only compare)

docker build --target runtime -t vibrance:local .
docker run --rm vibrance:local   # prints: version: devel
docker run --rm --init -p 127.0.0.1:8090:8080 -e VIBRANCE_PUBLIC_ORIGIN=http://127.0.0.1:8090 \
  -e VIBRANCE_ADMIN_PASSWORD='choose a long password' vibrance:local serve
```

The last command runs the server until Ctrl-C. It keeps its state in `/var/lib/vibrance` (the database `vibrance.db`), which lasts as long as the container unless a volume is mounted there, and it reads the library from `/musiclib`, which is empty unless the data volume of MusicLib is mounted there (read-only). `http://127.0.0.1:8090/health/live` answers `{"status":"live"}`, and `/health/ready` answers `{"status":"ready"}` once the startup is complete. In the container, `vibrance healthcheck` asks `/health/ready` and exits 0 or 1.

## Configuration

The server is configured only through its environment. An invalid configuration stops it at once with exit code 2 and one `config_invalid` log line that names every invalid variable. The server also refuses to run as root (`run_as_root`).

| Variable | Default | Meaning |
|---|---|---|
| `VIBRANCE_PUBLIC_ORIGIN` | none: required | The exact origin clients reach the server with, `scheme://host[:port]`: lowercase, no trailing slash, no default port. |
| `VIBRANCE_HTTP_ADDR` | `:8080` | The listen address in the container. |
| `VIBRANCE_ADMIN_USERNAME` | `admin` | The name of the first admin, read only while the database has no account. |
| `VIBRANCE_ADMIN_PASSWORD` | empty | The password of the first admin (12 to 1024 bytes, no control characters): required while the database has no account, and ignored once it has one. |
| `VIBRANCE_SCAN_INTERVAL` | `5m` | The time between two scans of the library: a Go duration, `30s` or longer. |
| `VIBRANCE_WORKERS` | `max(1, min(4, CPUs))` | From 1 to 16: the `ffmpeg` and `ffprobe` processes that run at once, and the albums that are indexed at once. |

Logs are JSON lines on stdout. On SIGTERM or SIGINT the server waits up to 10 seconds for the open requests, then closes their connections, stops the scanner and the tools it is running, closes the database and exits with code 0.

## The database

The server keeps everything in one SQLite file, `/var/lib/vibrance/vibrance.db`, in WAL mode. The path is fixed, and the folder must be on a local disk: the write-ahead log does not work on network filesystems. At every start, before it is ready, the server checks that the folder is writable, opens the database and applies the migrations it carries. A failure stops it with exit code 1 and one log line with a stable `code`:

| `code` | Meaning |
|---|---|
| `state_unwritable` | The server cannot create files in `/var/lib/vibrance`: the volume must belong to the user it runs as. |
| `store_open` | The database file cannot be opened, is not a database, or cannot be put in WAL mode. |
| `store_schema_too_new` | The database was written by a newer Vibrance. Migrations only go forward: use that version. |
| `store_migrate` | A migration could not be applied. Nothing of it is left behind. |

A server that is killed needs no repair: the next start recovers from the write-ahead log.

The schema is in `migrations/` (goose, embedded in the binary), the queries in `sql/`, and the Go code that `sqlc` generates from both is committed in `internal/store`. After a change to either, run `scripts/sqlc.sh` and commit the result: the gate fails while it is out of date.

- The development containers belong to the Compose project `vibrance-dev` (`compose.dev.yaml`), never to `musiclib`. Its volumes are `vibrance-dev_testdata` (the ext4 `TMPDIR` of the tests), `vibrance-dev_go-build-cache` and `vibrance-dev_go-mod-cache`.
- The gate tests a snapshot of the tree taken when the `test` image is built. `dev.sh` works on the live sources.
- Two scripts run the real MusicLib 1.2.0, in the Compose project `vibrance-spike` (new volumes, random passwords, no published port), and delete that project when they end; they need network access. `scripts/make-fixture-library.sh` regenerates the fixture library `testdata/library-v1/` (see `testdata/FIXTURE.md`); `scripts/spike.sh` checks what Vibrance assumes about MusicLib and writes `docs/spike-report.md`.
- The containers run as your uid and gid (`VIBRANCE_DEV_UID`/`VIBRANCE_DEV_GID` override them, `0` is refused). `GATE_TEST_TIMEOUT` sets the `go test -timeout` of the gate (default `15m`).

## The API specification

The API is described in [api/openapi.yaml](api/openapi.yaml) (OpenAPI 3.0.3), written by hand before the code: every operation under `/api/v1`, its parameters, its responses and its errors. The Go types, the router and the interface the handlers implement are generated from it by `oapi-codegen` (`api/oapi-codegen.yaml`) into `internal/api/api.gen.go`, which is committed and never edited by hand. After a change to the specification, run `scripts/generate.sh` and commit the result: the gate fails while the generated file is out of date. `oapi-codegen` is pinned as a `tool` directive of `go.mod` and runs from the module cache of the image, without network.

The tests of `internal/api` hold the table of operations and the list of error codes of the design, and fail when the specification has an operation, a status or a code more or less. Until the step that implements it, an operation answers `501 not_implemented`.

## The HTTP boundary

Every request crosses the same boundary (`internal/httpx`) before it reaches an operation, and every refusal is JSON, `{"code": ..., "message": ..., "details": {...}}`, with a stable `code`:

| Check | Refusal |
|---|---|
| `Host` must be the host and port of `VIBRANCE_PUBLIC_ORIGIN`. | `421 host_not_allowed` |
| `Origin`, if sent, must be exactly `VIBRANCE_PUBLIC_ORIGIN` (`Origin: null` is not). | `403 origin_not_allowed` |
| A request other than GET and HEAD must carry `X-Vibrance-Request: 1`. | `403 request_header_required` |
| The path must exist, and take the method. | `404 not_found`, `405 method_not_allowed` with `Allow` |
| A body is at most 1 MiB. | `413 body_too_large` |
| A body is one JSON value in UTF-8, without duplicate keys, and matches the schema of the operation, which has no unknown keys; parameters match the specification; an id is a UUID in its canonical form. | `400 invalid_request` |

The two health endpoints are outside the first three checks: a probe asks the container by its address. `X-Forwarded-*` headers are never read, so a reverse proxy must pass `Host` unchanged. No CORS header is ever sent.

Every response carries `X-Request-Id` (a UUIDv7 the server makes), `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`; a JSON response also `Cache-Control: private, no-store` and `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`. An unexpected failure, a panic included, answers `500 internal` and nothing else: its cause is in the log, under the same `request_id`.

The access log is one line per request, with `request_id`, `method`, `route` (the pattern of the router, such as `GET /api/v1/tracks/{id}`, never the path), `status`, `duration_ms` and `bytes`, and `user_id` when the request carried a session. It never holds a path, a query string, a header or a body. It is at `INFO`, and at `DEBUG` (not written by default) for the audio, the covers and the health endpoints, which are asked all the time.

## Accounts and sessions

Accounts are made by an admin; there is no self sign-up. At its first start, on a database without accounts, the server creates the admin from `VIBRANCE_ADMIN_USERNAME` and `VIBRANCE_ADMIN_PASSWORD`; once an account exists the two variables are ignored, and changing them changes nothing. Without a valid password for the first admin the server does not start, with exit code 2:

| `code` | Meaning |
|---|---|
| `admin_password_missing` | The database has no account and `VIBRANCE_ADMIN_PASSWORD` is not set. |
| `admin_password_invalid` | It is shorter than 12 bytes, longer than 1024, not UTF-8, or holds a control character (a line break left in `.env`). |
| `admin_username_invalid` | `VIBRANCE_ADMIN_USERNAME` is not 3 to 32 characters among `a-z`, `0-9`, `.`, `_`, `-`, beginning with a letter or a digit. |

Passwords are stored as argon2id hashes in the PHC format (64 MiB, 3 passes), and checked one at a time: a refused sign-in holds the turn for one second, and a name that no account has costs the same as a wrong password and gets the same answer. A session is a random token of 32 bytes: the cookie `vibrance_session` of a browser (30 days) or the bearer token of another client (90 days), renewed to a full lifetime when it is used after half of it. The database keeps only its SHA-256. A change of password signs out the other sessions of the account; a reset signs out all of them. Expired sessions are deleted at the start and every hour.

Every operation of the API but `GET /server`, `POST /auth/login` and `POST /auth/tokens` needs a session: `401 login_required` without one, and `403 forbidden` for an account that is not an admin on the operations under `/admin/`. When a request carries both a bearer token and the cookie, the bearer token counts. A session is used the way it was made: the token of `POST /auth/tokens` as a bearer token, the cookie of `POST /auth/login` as the cookie. Until the server is ready the operations answer `503 not_ready`. The access log has the `user_id` of a request that carried a session.

`POST /auth/login` sets the cookie with `HttpOnly`, `SameSite=Strict`, `Path=/` and `Max-Age` of 30 days from the sign-in, and `Secure` only when `VIBRANCE_PUBLIC_ORIGIN` is `https` (a browser drops a `Secure` cookie over plain http). `POST /auth/logout` revokes the session of the request and expires the cookie. An admin manages the accounts under `/admin/users`: the last enabled admin cannot be demoted, disabled or deleted (`409 last_admin`), and an admin cannot disable or delete their own account (`409 cannot_modify_self`); disabling an account, or setting its password, signs it out at once, and deleting it deletes its sessions, favorites and playlists. An admin sees no favorites or playlists but their own.

The accounts can also be managed from the machine of the server, while it runs:

```sh
printf '%s' "$PASSWORD" | docker exec -i <container> vibrance user create --username anna --role user --password-stdin
printf '%s' "$PASSWORD" | docker exec -i <container> vibrance user reset-password --username anna --password-stdin
docker exec <container> vibrance user list
```

The password is read from standard input only, never from the command line; a final line break is dropped. `reset-password` finds the account as a sign-in does, without regard to the case of the name. A reset signs the user out at once. The exit code is 0 when done, 2 for arguments, a name or a password that are not valid, and 1 otherwise (`username_taken`, `user_not_found`).

## The media tools

Vibrance reads the tags of the tracks with `ffprobe` and computes their audio fingerprint with `ffmpeg` (`internal/media`). The indexer that brings one album into the database with them, and the scanner that runs it over the library, are in `internal/library`. Both tools are in the image, at `/usr/local/bin`, and are the tools MusicLib verified the audio with. At every start, after the database is open and before it is ready, the server runs `ffmpeg -version` and `ffprobe -version` and refuses any version other than `8.1.3-musiclib1`. A refusal stops it with exit code 1 and one log line:

| `code` | Meaning |
|---|---|
| `media_tool_unavailable` | `ffmpeg` or `ffprobe` cannot be started, or does not print its version. |
| `media_tool_version` | The tool is not the pinned version: the image was built with other binaries. |

At most `VIBRANCE_WORKERS` of these processes run at once. Each one gets the file it reads as an open descriptor, never as a path, runs with a timeout, and is killed with everything it started when the timeout passes, when the server stops, or if the server is killed.

## The library

Vibrance reads the folder `/musiclib`, the data volume of MusicLib mounted read-only: `library/` and, only as signals, the two markers `.maintenance` and `.musiclib-store`. It never writes there. The path is fixed.

The scanner keeps the index in the database aligned with `library/`. A cycle runs when the server starts, then after every `VIBRANCE_SCAN_INTERVAL`, and sooner when something asks for one; requests coalesce, so one cycle runs and at most one waits. A cycle lists the album folders, reads their receipts, indexes the albums that are new or changed (`VIBRANCE_WORKERS` at once, each in one transaction), and marks the albums that are no longer there as unavailable. It never deletes a row: an album or a track that comes back has the id it had. A cycle can be interrupted at any point; the next one goes on from what was committed.

| State | Meaning |
|---|---|
| `idle` | No cycle is running. |
| `scanning` | A cycle is running. |
| `maintenance` | `/musiclib/.maintenance` exists: MusicLib is rebuilding or restoring its library. The index is left as it is. |
| `unavailable` | `/musiclib/.musiclib-store` is missing (the folder is not MusicLib's volume), or `library/` cannot be listed. The index is left as it is. |

The server is ready whatever the state of the library: with MusicLib stopped, or with an empty `/musiclib`, everything else works.

An album that cannot be indexed is listed among the problems of the library with a stable code (`receipt_missing`, `receipt_invalid`, `receipt_schema_unsupported`, `receipt_too_large`, `file_missing`, `file_size_mismatch`, `probe_failed`, `fingerprint_failed`, `listing_failed`), and nothing of it is written; the warnings `cover_invalid` and `tags_incomplete` do not keep an album out. An album that `ffprobe` or `ffmpeg` could not read is not examined again until its receipt changes or the server restarts.

When MusicLib moves a track to another album, or an album is deleted and imported again, the track gets a new id in Vibrance. At the end of every cycle the playlist items and the favorites that point to a track that is no longer available move to an available track with the same audio, if there is one: the item keeps its place in the playlist and shows the new title, artist and album.

Two more things happen by themselves. When the image carries another `ffmpeg` than the one that computed the audio fingerprints, they are computed again in the background, on the same rows. When it carries another version of the collation tables, the sort keys are computed again at the start, before the server is ready.

Each cycle logs one line, `scan finished` with its counters or `scan skipped` with the state. At the start, two more refusals are possible, both with exit code 1:

| `code` | Meaning |
|---|---|
| `library_index` | The index of the library could not be prepared (its sort keys could not be read or written). |
| `musiclib_folder` | The folder `/musiclib` does not exist in the container. An empty one is fine. |

## The covers

A cover belongs to an album: it is the `cover.jpg` or `cover.png` MusicLib wrote in the album folder (`internal/covers`). It is served as it is, or as a JPEG thumbnail that fits in a square of 256 or 640 pixels, with the proportions of the cover, never enlarged, on white where the cover is transparent.

Thumbnails are kept in `/var/lib/vibrance/thumbs/<first two characters of the hash>/<hash>_<size>.jpg`, where the hash is the SHA-256 of the cover file. The folder is only a cache: it can be removed at any time, also while the server runs, and nothing removes old thumbnails from it (about 120 KB for each album). A thumbnail is made when the scanner indexes an album with a new cover, in the background and one cover at a time, or at the first request for it. At most two covers are decoded at once, and a thumbnail asked for by several requests at the same moment is made once. A file of the cache that is not a whole thumbnail is made again.

The original is served in place of the thumbnail when the cover file is larger than 20 MiB or has more than 40 megapixels, and when the thumbnail cannot be made or written (the log says why, at `WARN`). A cover file that is no longer the one the scanner saw (another size or time for the original, other bytes for a thumbnail) is never served: MusicLib replaced the album. The request answers `503 library_changing` with `Retry-After: 5` and starts a scan, which brings the index up to date.

## The audio and the lyrics

`GET /api/v1/tracks/{id}/audio` sends the file MusicLib wrote, as it is, with `http.ServeContent`: ranges, `If-Range`, `If-None-Match` and `HEAD` work as in any static file server, the `ETag` is the SHA-256 of the file and the response is `Cache-Control: private, no-cache`. A stream has no write deadline: the `WriteTimeout` of the server does not cut a long track. The file is read only from the path the scanner recorded, through the same confined folder as the scanner, and must still have the size and the time the scanner saw: otherwise MusicLib replaced it, and the request answers `503 library_changing` with `Retry-After: 5` and starts a scan. When `library/` is not there at all, or a file cannot be opened, the track answers `404 track_unavailable` and nothing is scanned.

`GET /api/v1/tracks/{id}/lyrics` reads the `.lrc` file of the track (at most 2 MiB), checks its SHA-256 against the index and answers its lines as JSON (`internal/lyrics`).

## Pinned versions

Every image is pinned by exact version and by digest. The pins are copied from MusicLib 1.2.0: Go 1.25.14 on Debian trixie, the `debian:trixie-20260918-slim` runtime base, Dockerfile frontend 1.26.0, shellcheck 0.11.0 and sqlc 1.31.1. `ffmpeg` and `ffprobe` (`8.1.3-musiclib1`) are copied from the published image `ghcr.io/tommasonovelli/musiclib:1.2.0`. The digests are in the `Dockerfile`, `scripts/lint-shell.sh` and `scripts/lib/common.sh`. The Go modules are at exact versions in `go.mod`, `oapi-codegen` among them. [docs/compat.md](docs/compat.md) lists the MusicLib versions Vibrance works with.

## License

MIT, see [LICENSE](LICENSE).
