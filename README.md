# Vibrance

Vibrance is the listening side of [Vibrance MusicLib](https://github.com/tommasonovelli/vibrance-musiclib). It is a read-only server over the `library/` folder that MusicLib produces, with multiple users, favorites, playlists, full-text search and an HTTP API documented with OpenAPI. The two products share only that folder.

**Status:** in development toward v0.1. So far the server starts, answers its health endpoints and stops; it has no database, no library and no API yet.

## Trying it

Everything is built, tested and run in Docker. The host needs only Docker Engine or Docker Desktop with the Compose v2 plugin: no Go and no FFmpeg. On Windows, run the scripts from Git Bash.

```sh
scripts/check.sh                 # the gate: go build, go vet, gofmt, go test -race (no network)
scripts/check.sh ./internal/media/...   # the gate on one package tree
scripts/dev.sh                   # a shell in the toolchain container, on the live sources
scripts/dev.sh go test -race -count=20 -run TestX ./internal/media
scripts/lint-shell.sh            # shellcheck on every shell script

docker build --target runtime -t vibrance:local .
docker run --rm vibrance:local   # prints: version: devel
docker run --rm --init -p 127.0.0.1:8090:8080 -e VIBRANCE_PUBLIC_ORIGIN=http://127.0.0.1:8090 vibrance:local serve
```

The last command runs the server until Ctrl-C. `http://127.0.0.1:8090/health/live` answers `{"status":"live"}`, and `/health/ready` answers `{"status":"ready"}` once the startup is complete. In the container, `vibrance healthcheck` asks `/health/ready` and exits 0 or 1.

## Configuration

The server is configured only through its environment. An invalid configuration stops it at once with exit code 2 and one `config_invalid` log line that names every invalid variable. The server also refuses to run as root (`run_as_root`).

| Variable | Default | Meaning |
|---|---|---|
| `VIBRANCE_PUBLIC_ORIGIN` | none: required | The exact origin clients reach the server with, `scheme://host[:port]`: lowercase, no trailing slash, no default port. |
| `VIBRANCE_HTTP_ADDR` | `:8080` | The listen address in the container. |
| `VIBRANCE_SCAN_INTERVAL` | `5m` | A Go duration, `30s` or longer. |
| `VIBRANCE_WORKERS` | `max(1, min(4, CPUs))` | From 1 to 16. |

Logs are JSON lines on stdout. On SIGTERM or SIGINT the server waits up to 10 seconds for the open requests, then closes their connections and exits with code 0.

- The development containers belong to the Compose project `vibrance-dev` (`compose.dev.yaml`), never to `musiclib`. Its volumes are `vibrance-dev_testdata` (the ext4 `TMPDIR` of the tests), `vibrance-dev_go-build-cache` and `vibrance-dev_go-mod-cache`.
- The gate tests a snapshot of the tree taken when the `test` image is built. `dev.sh` works on the live sources.
- Two scripts run the real MusicLib 1.1.0, in the Compose project `vibrance-spike` (new volumes, random passwords, no published port), and delete that project when they end; they need network access. `scripts/make-fixture-library.sh` regenerates the fixture library `testdata/library-v1/` (see `testdata/FIXTURE.md`); `scripts/spike.sh` checks what Vibrance assumes about MusicLib and writes `docs/spike-report.md`.
- The containers run as your uid and gid (`VIBRANCE_DEV_UID`/`VIBRANCE_DEV_GID` override them, `0` is refused). `GATE_TEST_TIMEOUT` sets the `go test -timeout` of the gate (default `15m`).

## Pinned versions

Every image is pinned by exact version and by digest. The pins are copied from MusicLib 1.1.0: Go 1.25.14 on Debian trixie, the `debian:trixie-20260918-slim` runtime base, Dockerfile frontend 1.26.0 and shellcheck 0.11.0. `ffmpeg` and `ffprobe` (`8.1.3-musiclib1`) are copied from the published image `ghcr.io/tommasonovelli/musiclib:1.1.0`. The digests are in the `Dockerfile` and `scripts/lint-shell.sh`. [docs/compat.md](docs/compat.md) lists the MusicLib versions Vibrance works with.

## License

MIT, see [LICENSE](LICENSE).
