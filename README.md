# Vibrance

Vibrance is the listening side of [Vibrance MusicLib](https://github.com/tommasonovelli/vibrance-musiclib). It is a read-only server over the `library/` folder that MusicLib produces, with multiple users, favorites, playlists, full-text search and an HTTP API documented with OpenAPI. The two products share only that folder.

**Status:** in development toward v0.1. So far the server starts, creates and migrates its SQLite database, checks its `ffmpeg` and `ffprobe`, keeps an index of the MusicLib library up to date in the background, answers its health endpoints and stops; it has no API yet.

## Trying it

Everything is built, tested and run in Docker. The host needs only Docker Engine or Docker Desktop with the Compose v2 plugin: no Go and no FFmpeg. On Windows, run the scripts from Git Bash.

```sh
scripts/check.sh                 # the gate: sqlc diff, go build, go vet, gofmt, go test -race (no network)
scripts/check.sh ./internal/media/...   # the gate on one package tree
scripts/dev.sh                   # a shell in the toolchain container, on the live sources
scripts/dev.sh go test -race -count=20 -run TestX ./internal/media
scripts/lint-shell.sh            # shellcheck on every shell script
scripts/fuzz.sh FuzzParseReceipt 60s ./internal/library   # one fuzz target, for a duration, on the live sources
scripts/sqlc.sh                  # regenerate internal/store from sql/ and the migrations (sqlc.sh diff: only compare)

docker build --target runtime -t vibrance:local .
docker run --rm vibrance:local   # prints: version: devel
docker run --rm --init -p 127.0.0.1:8090:8080 -e VIBRANCE_PUBLIC_ORIGIN=http://127.0.0.1:8090 vibrance:local serve
```

The last command runs the server until Ctrl-C. It keeps its state in `/var/lib/vibrance` (the database `vibrance.db`), which lasts as long as the container unless a volume is mounted there, and it reads the library from `/musiclib`, which is empty unless the data volume of MusicLib is mounted there (read-only). `http://127.0.0.1:8090/health/live` answers `{"status":"live"}`, and `/health/ready` answers `{"status":"ready"}` once the startup is complete. In the container, `vibrance healthcheck` asks `/health/ready` and exits 0 or 1.

## Configuration

The server is configured only through its environment. An invalid configuration stops it at once with exit code 2 and one `config_invalid` log line that names every invalid variable. The server also refuses to run as root (`run_as_root`).

| Variable | Default | Meaning |
|---|---|---|
| `VIBRANCE_PUBLIC_ORIGIN` | none: required | The exact origin clients reach the server with, `scheme://host[:port]`: lowercase, no trailing slash, no default port. |
| `VIBRANCE_HTTP_ADDR` | `:8080` | The listen address in the container. |
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

## Pinned versions

Every image is pinned by exact version and by digest. The pins are copied from MusicLib 1.2.0: Go 1.25.14 on Debian trixie, the `debian:trixie-20260918-slim` runtime base, Dockerfile frontend 1.26.0, shellcheck 0.11.0 and sqlc 1.31.1. `ffmpeg` and `ffprobe` (`8.1.3-musiclib1`) are copied from the published image `ghcr.io/tommasonovelli/musiclib:1.2.0`. The digests are in the `Dockerfile`, `scripts/lint-shell.sh` and `scripts/lib/common.sh`. The Go modules are at exact versions in `go.mod`. [docs/compat.md](docs/compat.md) lists the MusicLib versions Vibrance works with.

## License

MIT, see [LICENSE](LICENSE).
