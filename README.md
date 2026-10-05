# Vibrance

Vibrance is the listening side of [Vibrance MusicLib](https://github.com/tommasonovelli/vibrance-musiclib). It is a read-only server over the `library/` folder that MusicLib produces, with multiple users, favorites, playlists, full-text search and an HTTP API documented with OpenAPI. The two products share only that folder.

**Status:** in development toward v0.1. The server keeps an index of the MusicLib library up to date in the background and serves the whole API of `api/openapi.yaml`: accounts and sessions, the catalog, the audio, the covers and the lyrics, the search, the favorites, the playlists and the state of the library. It also serves that specification and a page that documents it. The commands `backup`, `restore` and `doctor` save, restore and check its database, and the Compose stack runs it next to MusicLib: [docs/operations.md](docs/operations.md) is the guide to install and run it. The contract suite runs it against the real MusicLib ([docs/compat.md](docs/compat.md)). What is still to come is the release.

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
scripts/check-compose-sync.sh    # compose.yaml and env.example are MusicLib's files plus Vibrance's blocks (network)
scripts/stack-smoke.sh           # the Compose stack for real, in the project vibrance-contract (network, ~10 minutes)
scripts/contract.sh              # the contract suite with the real MusicLib, in the project vibrance-contract (network, a few minutes)

docker build --target runtime -t vibrance:local .
docker run --rm vibrance:local version   # prints: version: devel
docker run --rm --init -p 127.0.0.1:8090:8080 -e VIBRANCE_PUBLIC_ORIGIN=http://127.0.0.1:8090 \
  -e VIBRANCE_ADMIN_PASSWORD='choose a long password' vibrance:local
```

The last command runs the server, the default command of the image, until Ctrl-C. It keeps its state in `/var/lib/vibrance` (the database `vibrance.db`), which lasts as long as the container unless a volume is mounted there, and it reads the library from `/musiclib`, which is empty unless the data volume of MusicLib is mounted there (read-only). `http://127.0.0.1:8090/health/live` answers `{"status":"live"}`, and `/health/ready` answers `{"status":"ready"}` once the startup is complete. In the container, `vibrance healthcheck` asks `/health/ready` and exits 0 or 1. `http://127.0.0.1:8090/` leads to the documentation of the API, and [docs/api.md](docs/api.md) is a short tour of the API with `curl`.

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

### Backup, restore and doctor

The whole database is precious: the ids of the tracks, which playlists and favorites point to, cannot be rebuilt from the library. Three commands of the image save, restore and check it; [docs/operations.md](docs/operations.md) shows them in the Compose stack. `/backup` is the backup folder (`VIBRANCE_BACKUP`); the thumbnails are a cache and are not saved.

```sh
docker exec <container> vibrance backup --to "/backup/$(date +%F-%H%M)"   # while the server runs
docker exec <container> vibrance doctor                                   # while the server runs; changes nothing
vibrance restore --from /backup/2026-10-05-2130                           # in a one-off container, server stopped, empty state volume
```

- `backup` copies the database with `VACUUM INTO`, a consistent copy also while the server writes, into a new folder: `vibrance.db` (mode 0600: it holds the password hashes) and `manifest.json` (the version of Vibrance and of the schema, the date, the size and SHA-256 of the copy, the number of users, playlists, playlist items, favorites, artists, albums and tracks). It checks the copy with `PRAGMA integrity_check` and reads it again for its SHA-256. It writes under `.vibrance-backup-*.tmp` and gives the folder its name only when it is complete: a backup that fails or is killed leaves only that temporary folder, which you remove. It prints `Backup completed: /backup/NAME`.
- `restore` puts the copy in place as `/var/lib/vibrance/vibrance.db` only when the state folder has no database (nor a `-wal`, `-shm` or `-journal` file). It checks the manifest strictly and the SHA-256 of the copy, refuses a copy made by a newer Vibrance, writes and syncs a temporary file and gives it its name without ever overwriting one. A copy of an older schema is migrated by the server when it starts. It prints `Restore completed: /backup/NAME. Start the server.`
- `doctor` reads the database in one transaction: `PRAGMA integrity_check`, `PRAGMA foreign_key_check`, the integrity of each full-text table, every full-text row against an available artist, album or track with the same names and the other way round, `track_count` and `duration_ms` of every album against its available tracks, and at least one enabled admin. It prints one line per finding, `code entity: message. Advice: ...`, and a last line: `Doctor complete: no damage found.` (exit 0) or `Doctor complete: N problems found. Nothing was changed.` (exit 1). It repairs nothing.

Exit codes: 0 done; 2 refused before anything was written (bad arguments, root, the codes marked *refusal* below); 1 failed or damage found. A failure is logged with its `code` and an `advice`.

| `code` | Command | Meaning |
|---|---|---|
| `backup_outside_backup` (refusal) | backup | The destination is not a plain absolute path of a new folder under `/backup`. |
| `backup_exists` (refusal) | backup | A file or folder with that name exists: backups are never overwritten. |
| `backup_destination` (refusal) | backup | The parent folder does not exist or cannot be written. |
| `database_missing` (refusal) | backup, doctor | There is no `vibrance.db` in the state folder. |
| `store_schema_old` (refusal) | backup, doctor | The database was written by an older Vibrance: start the server once, which migrates it. |
| `store_schema_too_new`, `store_open` (refusal) | backup, doctor | The database is newer than this binary, or is not a database. |
| `backup_failed`, `backup_verify` | backup | The copy could not be written, or is damaged: remove the temporary folder, run `doctor`, retry. |
| `restore_outside_backup` (refusal) | restore | The backup is not a folder under `/backup`. |
| `restore_database_exists` (refusal) | restore | The state folder has a database, or what is left of one: restore into a new, empty volume. |
| `restore_manifest_invalid`, `restore_hash`, `restore_schema_too_new` (refusal) | restore | The backup does not pass its checks, or a newer Vibrance made it: use another backup, or that version. |
| `restore_destination` (refusal), `restore_failed` | restore | The state folder cannot be written, or writing failed: nothing was restored. |
| `doctor_failed` | doctor | The inspection could not be completed. |
| `doctor_integrity`, `doctor_foreign_key`, `doctor_search_index` | doctor | The file is damaged: keep it, and restore the latest backup that `doctor` finds sound. |
| `doctor_search_extra`, `doctor_search_missing`, `doctor_search_stale` | doctor | A full-text row does not agree with the index: searches that meet it fail or miss it. Nothing in Vibrance repairs it; restore a sound backup. |
| `doctor_album_counters` | doctor | An album shows a wrong number of tracks or duration until it is indexed again. |
| `doctor_no_admin` | doctor | No admin is enabled: `vibrance user create --role admin`. |

The schema is in `migrations/` (goose, embedded in the binary), the queries in `sql/`, and the Go code that `sqlc` generates from both is committed in `internal/store`. After a change to either, run `scripts/sqlc.sh` and commit the result: the gate fails while it is out of date.

- The development containers belong to the Compose project `vibrance-dev` (`compose.dev.yaml`), never to `musiclib`. Its volumes are `vibrance-dev_testdata` (the ext4 `TMPDIR` of the tests), `vibrance-dev_go-build-cache` and `vibrance-dev_go-mod-cache`.
- The gate tests a snapshot of the tree taken when the `test` image is built. `dev.sh` works on the live sources.
- Two scripts run the real MusicLib 1.2.0, in the Compose project `vibrance-spike` (new volumes, random passwords, no published port), and delete that project when they end; they need network access. `scripts/make-fixture-library.sh` regenerates the fixture library `testdata/library-v1/` (see `testdata/FIXTURE.md`); `scripts/spike.sh` checks what Vibrance assumes about MusicLib and writes `docs/spike-report.md`.
- The files of an installation are `compose.yaml` and `env.example` (MusicLib's own files of the pinned release, unchanged, plus blocks between `# >>> Vibrance` and `# <<< Vibrance`), `compose.caddy.yaml` and `Caddyfile.example`; [docs/operations.md](docs/operations.md) explains them. `scripts/check-compose-sync.sh` downloads MusicLib's two files of the release the Dockerfile pins (`ARG MUSICLIB_IMAGE`, the one place that names MusicLib's version) and fails when, outside the blocks, a byte differs, or when a block changes what Compose makes of MusicLib's services. `scripts/stack-smoke.sh` runs the stack in the Compose project `vibrance-contract`, with new volumes, random passwords and no port published on the host, and deletes that project when it ends: it installs from scratch in both start orders, imports an album into MusicLib and waits for Vibrance to list it, stops MusicLib and checks that Vibrance does not change, makes a backup, checks that the sync check turns red on an altered file, and puts Caddy with `tls internal` in front of both. `scripts/contract.sh` runs, in the same project and with the same care, the scenarios A1–A20 of the contract with MusicLib: the test of `internal/contract` (build tag `contract`) changes albums through the API of the real MusicLib and checks through Vibrance's API that tracks keep their ids and that playlists and favorites follow; [docs/compat.md](docs/compat.md) says what it covers. It is not part of the gate: run it before a release and after any change of MusicLib's version.
- The containers run as your uid and gid (`VIBRANCE_DEV_UID`/`VIBRANCE_DEV_GID` override them, `0` is refused). `GATE_TEST_TIMEOUT` sets the `go test -timeout` of the gate (default `15m`).

## The API specification

The API is described in [api/openapi.yaml](api/openapi.yaml) (OpenAPI 3.0.3), written by hand before the code: every operation under `/api/v1`, its parameters, its responses and its errors. The Go types, the router and the interface the handlers implement are generated from it by `oapi-codegen` (`api/oapi-codegen.yaml`) into `internal/api/api.gen.go`, which is committed and never edited by hand. After a change to the specification, run `scripts/generate.sh` and commit the result: the gate fails while the generated file is out of date. `oapi-codegen` is pinned as a `tool` directive of `go.mod` and runs from the module cache of the image, without network.

The tests of `internal/api` hold the table of operations and the list of error codes of the design, and fail when the specification has an operation, a status or a code more or less. The tests of `internal/app` hold the authorization matrix, one row per operation, and check every response they look at against the specification.

The server serves its own documentation, without a session:

- `GET /api/openapi.yaml` is the specification, the bytes of `api/openapi.yaml` the binary was built with: the same ones the requests are validated against.
- `GET /api/docs` is a page that shows it and can send requests to the server, and `GET /` redirects there. The page is `web/docs/index.html` and loads one script, a vendored release of [Scalar API Reference](https://github.com/scalar/scalar) served at `/api/docs/scalar.js`. [web/docs/VENDOR.md](web/docs/VENDOR.md) records its version, its source and its SHA-256, and a test fails when the file is another. Nothing comes from another host: the page has its own `Content-Security-Policy`, which allows the script of the server, inline styles and requests to the server, and nothing else. To try a request other than GET from the page, add the header `X-Vibrance-Request: 1` in its request client.

[docs/api.md](docs/api.md) shows the main operations with `curl`.

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

Every response carries `X-Request-Id` (a UUIDv7 the server makes), `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`; a JSON response also `Cache-Control: private, no-store` and `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`. The page of the documentation is the one response with a wider policy, and the files of the documentation are `Cache-Control: no-cache` with an `ETag`. An unexpected failure, a panic included, answers `500 internal` and nothing else: its cause is in the log, under the same `request_id`.

A few answers come from Go's HTTP server before any of this runs, as plain text and without `X-Request-Id`: `431` for headers larger than 64 KiB, and `400` for a request line or a `Host` header that cannot be parsed.

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

An admin reads the state with `GET /api/v1/admin/library`: the state above, when the last cycle that went through the library started and ended and whether it reached its end, how many albums and tracks are available and unavailable, how far a running cycle has come, whether MusicLib is in maintenance, and the problems of the last cycle (at most 200, kept in memory: the list is empty after a restart until the first cycle ends). `POST /api/v1/admin/library/scan` asks for a cycle and answers `202` at once with the state as it is; it is the only operation that makes the scanner do anything, and it coalesces like every other request for a cycle.

An album that cannot be indexed is listed among the problems of the library with a stable code (`receipt_missing`, `receipt_invalid`, `receipt_schema_unsupported`, `receipt_too_large`, `file_missing`, `file_size_mismatch`, `probe_failed`, `fingerprint_failed`, `listing_failed`), and nothing of it is written; the warnings `cover_invalid` and `tags_incomplete` do not keep an album out. An album that `ffprobe` or `ffmpeg` could not read is not examined again until its receipt changes or the server restarts.

An album whose folder and receipt have not changed is not looked at again, with one exception. When a request for the audio, the cover or the lyrics finds a file that is not the one the index describes, it asks the scanner to index the album of that file again. That cycle reads the size and the time of every file of the album from the disk and matches the files to their rows by SHA-256, without running `ffprobe` or `ffmpeg`, so files that only have another modification time (a volume copied or restored without its times, a `touch`) are served again after one cycle, with the same ids. An album that `ffprobe` or `ffmpeg` could not read is not examined again for such a request either.

One state does not heal by itself: a file whose bytes are not those of its receipt (another size, which the scanner reports as `file_size_mismatch`, or lyrics with another SHA-256) while the receipt is unchanged. The album stays as it was indexed and every request for that file answers `503 library_changing` and asks for a cycle. MusicLib does not leave a library in this state, because every change it makes writes a new receipt; render the album again in MusicLib to repair it.

The match is by the SHA-256 the receipt records: the bytes of the files are not read again. So a file changed by hand into other bytes of the same size, under an unchanged receipt (a tag editor that writes into the padding of a FLAC file), answers `503` once and is then served under the `ETag` of the receipt, with the metadata the index had. Neither does the first indexing read the bytes against the receipt: the receipt is what MusicLib verified.

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

The original is served in place of the thumbnail when the cover file is larger than 20 MiB or has more than 40 megapixels, and when the thumbnail cannot be made or written (the log says why, at `WARN`). A cover file that is no longer the one the scanner saw (another size or time for the original, other bytes for a thumbnail) is never served: MusicLib replaced the album. The request answers `503 library_changing` with `Retry-After: 5` and asks the scanner to index the album again, which brings the index up to date. When `library/` is not there at all, or the cover file cannot be opened (a symbolic link, a folder, a permission), the cover answers `404 cover_not_found` and nothing is scanned; a thumbnail already in the cache is still served.

## The audio and the lyrics

`GET /api/v1/tracks/{id}/audio` sends the file MusicLib wrote, as it is, with `http.ServeContent`: ranges, `If-Range`, `If-None-Match` and `HEAD` work as in any static file server, the `ETag` is the SHA-256 of the file and the response is `Cache-Control: private, no-cache`. A stream has no write deadline: the `WriteTimeout` of the server does not cut a long track. The file is read only from the path the scanner recorded, through the same confined folder as the scanner, and must still have the size and the time the scanner saw: otherwise it was replaced, and the request answers `503 library_changing` with `Retry-After: 5` and asks the scanner to index its album again. A file that is gone while `library/` is there, or that is replaced while it is being opened, is answered the same way. When `library/` is not there at all, or a file cannot be opened, the track answers `404 track_unavailable` and nothing is scanned.

`GET /api/v1/tracks/{id}/lyrics` reads the `.lrc` file of the track (at most 2 MiB), checks its SHA-256 against the index and answers its lines as JSON (`internal/lyrics`). Other bytes answer `503 library_changing` like the audio; a lyrics file that is gone or cannot be read answers `404 lyrics_not_found`.

## The search

`GET /api/v1/search?q=...` searches the artists, the albums and the tracks that are available, with the full-text index of SQLite (FTS5, `internal/search`), which the scanner keeps in the same transactions as the index. A result has every word of `q`, each as the beginning of one of its words, so the search works while the user types: `mil dav` finds Miles Davis. Case and accents do not count. Nothing in `q` is an operator: quotes, `*`, `-`, `OR` and the like are separators or plain words.

`q` is split into words only at the ASCII characters that are not letters or digits, at the Unicode spaces and at the control characters; at most 8 words of at most 64 characters count. Inside a word SQLite's tokenizer decides, exactly as it did for the names in the index. This has effects worth knowing:

- Words joined by punctuation that is not ASCII are a phrase, in their order: `Sigur—Rós` finds Sigur Rós and `Rós—Sigur` does not, while `rós sigur` does.
- A word made only of such punctuation is left out; alone, it finds nothing, without an error.
- The emoji and symbols the tokenizer does not know as such (most recent emoji) are part of the word they touch, in the names and in `q`: Disco🪩Ball is found by `disco🪩` and not by `disco ball`.
- An accent sent as a separate character is found like the letter that carries it for Latin letters, not for every script: a kana with a separate voicing mark does not find the composed name.
- There is no tolerance for typing mistakes, and a run of Chinese or Japanese characters is one word, found only from its beginning.

## The playlists

A playlist belongs to the user who made it and to nobody else: for every other account, an admin included, it answers `404 playlist_not_found`. A user has at most 500 playlists, and a playlist at most 10,000 items; the same track can be in a playlist more than once, because each item has an id of its own.

Every change of a playlist, of its name or of its items, gives it a new `revision` and a new entity tag, `"playlist:<id>:<revision>"`, which is both in the `ETag` header and in the `etag` field of the body. Adding tracks at the end, renaming, deleting and removing an item take `If-Match` when it is sent and do not need it. Inserting at a position and moving an item need it (`428 precondition_required` without it), because a position means something only for a known revision. A tag of another revision answers `412 precondition_failed` and changes nothing; the comparison and the change are one transaction, so of two clients that change a playlist with the same tag only one succeeds.

The positions of the items are always `0` to `item_count - 1`, without gaps. A track that is no longer available stays where it was, with `"available": false`: it counts in `item_count` and for the positions, and not in `duration_ms`. A request that adds several tracks adds all of them or none (`422 unknown_track` and `422 track_unavailable` list the ids they refuse).

## Pinned versions

Every image is pinned by exact version and by digest. The pins are copied from MusicLib 1.2.0: Go 1.25.14 on Debian trixie, the `debian:trixie-20260918-slim` runtime base, Dockerfile frontend 1.26.0, shellcheck 0.11.0 and sqlc 1.31.1. `ffmpeg` and `ffprobe` (`8.1.3-musiclib1`) are copied from the published image `ghcr.io/tommasonovelli/musiclib:1.2.0`. The digests are in the `Dockerfile`, `scripts/lint-shell.sh` and `scripts/lib/common.sh`. The Go modules are at exact versions in `go.mod`, `oapi-codegen` among them. The script of the documentation page is Scalar API Reference 1.72.4, with its SHA-256 in `web/docs/VENDOR.md`. `compose.caddy.yaml` pins Caddy 2.11.4 (`caddy:2.11.4-builder-alpine` and `caddy:2.11.4-alpine`, by digest) and the plugin `github.com/caddy-dns/cloudflare` v0.2.4. In `compose.yaml` the images of MusicLib's services are MusicLib's own lines (PostgreSQL by digest, MusicLib by its release version), and Vibrance's image is named by its release version, as MusicLib names its own. [docs/compat.md](docs/compat.md) lists the MusicLib versions Vibrance works with.

## License

MIT, see [LICENSE](LICENSE).
