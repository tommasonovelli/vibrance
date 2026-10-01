# Vibrance: engineering notes

A short log of decisions, deviations, risks and questions, kept by the engineers (DESIGN.md §0.10, Appendix C.2). One entry per topic, 2–5 lines, never a diary. `TO CONFIRM` entries are reported to the user at the end of the phase. Once decided, they become `RESOLVED`.

Format:

```markdown
## N-001 · Short title — DECIDED | TO CONFIRM | RESOLVED
Step: S7. Context: what the design did not say, or contradicted. Choice: what was done.
Reason: why it is the simplest or most conservative reading. Visible effect: none | which.
```

## N-001 · Repository structure of §3.3 in S0 — DECIDED
Step: S0. Context: "structure of §3.3 (folders with a `doc.go` only where needed)". Git does not track empty folders.
Choice: each Go package folder of §3.3 exists, with a `doc.go` that states its role and dependency rules; `cmd/vibrance` and `internal/buildinfo` have real code. The non-Go folders (`api/`, `migrations/`, `sql/`, `testdata/`) are created by the steps that fill them (S1, S3, S11), with no placeholder files.
Reason: no empty placeholders, and nothing exported. Visible effect: none.

## N-002 · Pin of the MusicLib image that provides ffmpeg — DECIDED
Step: S0. Pin: `ghcr.io/tommasonovelli/musiclib:1.1.0@sha256:14f63bcde28f4c586a775656f6b3e77e3f0129c2e1339f7e957dca04351f9cb1`. This is the digest of the multi-arch index (linux/amd64 manifest `sha256:07fea2cd…`), resolved on 2026-09-30 with `docker buildx imagetools inspect`. The image is public and can be pulled.
Reason: index digests, as for every other pin copied from MusicLib's `docs/docker.md`. Visible effect: none.

## N-003 · `go.sum` optional in the `deps` stage — DECIDED
Step: S0. The module has no dependencies yet, so there is no `go.sum`. The `deps` stage copies `go.mod go.sum*`: the glob matches nothing today and picks up `go.sum` once S3 adds modules. `go mod download && go mod verify` still run.
Reason: the smallest change from MusicLib's `COPY go.mod go.sum ./`, with no placeholder file. Visible effect: none.

## N-004 · `vibrance` CLI in S0 — DECIDED
Step: S0. `vibrance version` prints `version: <v>` and exits 0. Any other argument list is refused with exit 2 (§11.4) and one JSON log line on stdout (§11.5), `level=ERROR`, `code=usage`. If writing the version fails, the exit code is 1, with `code=version_output`. The runtime image has `ENTRYPOINT ["/usr/local/bin/vibrance"]` and `CMD ["version"]`: the command is `vibrance version`, as the step asks, and `docker run image <subcommand>` works as in MusicLib.
Reason: MusicLib's conventions (exit codes, `code` names, ENTRYPOINT). Visible effect: none beyond the step.

## N-005 · Executable bit of the shell scripts — DECIDED
Step: S0. The checkout on Windows has `core.filemode=false`, so a plain `git add` would record the scripts as 100644, and they would not run on Linux or in an image built from a Linux checkout. The engineer staged `scripts/*.sh` and `docker/*.sh` with `git add --chmod=+x`. This is an index entry, not a commit, and a later `git add -A` keeps the mode. `scripts/lib/common.sh` is sourced only and stays 100644, as in MusicLib.
Reason: this is the only way to record the mode from Windows. Visible effect: none.

## N-006 · Runtime image: license files and ownership — DECIDED
Step: S0. `/usr/share/doc/vibrance/` holds only `LICENSE` (0644) until S25 adds `THIRD_PARTY_NOTICES.md` and `licenses/` (the notices for ffmpeg and the Go modules). Nothing is published before S25. The folder is created with `install -d`, because `COPY --chmod` would give it the file's mode. The binary is copied `--chown=root:root`, so that the runtime user can never own it (MusicLib leaves it owned by the build user). No `EXPOSE` or `ENV` yet: the server arrives in S2.
Reason: I16, and the stated steps of the plan. Visible effect: none.

## N-007 · Pinned ffmpeg version and the gate in S0 — DECIDED
Step: S0. The pinned version `8.1.3-musiclib1` is an unexported constant of the test `internal/media.TestPinnedToolsInstalled` until S5 adds the adapter that checks it at startup, where it moves. S0's gate runs build, vet, gofmt and `go test -race`, as the step asks. The `sqlc diff` and `generate.sh diff` steps of §3.6 come with S3 and S11, and `scripts/fuzz.sh` with the first fuzz target. The `test` service has `network_mode: none`, because there is no test database to reach.
Reason: nothing exported and unused, and no script before it has anything to run (I16). Visible effect: none.

## N-008 · Gate build discards its output — DECIDED
Step: S0. MusicLib's gate runs a plain `go build <patterns>`: with a single `main` package (`scripts/check.sh ./cmd/vibrance`) Go writes the executable into the current directory, which fails on the read-only snapshot (`open vibrance: read-only file system`). Vibrance's `docker/gate.sh` runs `go build -o /dev/null <patterns>`, which works for any pattern.
Reason: the documented `scripts/check.sh [packages]` must give no false red and write nothing. Visible effect: none.

## N-009 · The S1 verification tool is a Go program in `scripts/spike/` — DECIDED
Step: S1. Context: "uno strumento di verifica (script o programma di prova, non di produzione)". The checks need MusicLib's JSON API, ffprobe JSON, a concurrent watcher of the library and receipts parsed strictly: too much for shell and `jq`. Choice: `package main` in `scripts/spike/` (stdlib only), run in a `tools` container by `scripts/spike.sh`; the host script does only what needs `docker` (H1, stopping the app, the offline `rebuild`). The gate builds, vets and tests it; its tests also check the committed fixture against its receipts. Nothing in `internal/` imports it.
Reason: one language, testable code, no new dependency. Visible effect: none.

## N-010 · The `vibrance-spike` Compose project — DECIDED
Step: S1. Choice: `scripts/spike/compose.yaml` (postgres and app as in MusicLib 1.1.0's `compose.yaml`, plus `tools` and `taggers`). No published port (the tools container calls `http://app:8080`, so a MusicLib on 127.0.0.1:8080 is never in the way); MusicLib and Debian images come from the Dockerfile's `ARG MUSICLIB_IMAGE`/`RUNTIME_IMAGE`, PostgreSQL's pin is copied from MusicLib. The random passwords go into a `.env` in a private temporary folder (`--env-file`), deleted at exit. Both scripts run `down --volumes` on `vibrance-spike` only, at start (leftovers) and at exit.
Reason: the step's "volumi nuovi; password casuale in un .env non committato", with one place per pin. Visible effect: none.

## N-011 · Inputs: LAME and AtomicParsley; ReplayGain on A–D — DECIDED
Step: S1. Context: §12.2 asks LAME for the MP3s, in a disposable Debian container. The pinned ffmpeg's ipod muxer drops keys it does not know, so it cannot write the iTunes freeform ReplayGain atoms of M4A. Choice: the `taggers` container installs `lame=3.100-6+b3` and `atomicparsley=20240608.083822.1ed9031-1` from the Debian snapshot MusicLib 1.1.0 uses (20260926T000000Z). §12.2 lists ReplayGain for album A; B, C and D carry it too, so H9 covers every codec and the fixture holds real ReplayGain keys for each format (useful to S5).
Reason: the only way to check H9 for MP3 and M4A; no Go dependency. Visible effect: none (test data).

## N-012 · `FIXTURE.md` is `testdata/FIXTURE.md` — DECIDED
Step: S1. Context: §12.2 does not say where `FIXTURE.md` goes. Choice: next to the folder, not inside it, so that `testdata/library-v1/` is exactly the `library/` MusicLib wrote (a file at the top of `library/` is something the scanner must skip, not part of the fixture). `.gitattributes` marks `testdata/library-v1/**` as `-text` (the S0 debt): a line-ending conversion would break the SHA-256 of the receipts, and `TestFixtureLibraryV1` would fail.
Reason: the fixture stays byte-identical to MusicLib's output. Visible effect: none.

## N-013 · The report is in English: CONFIRMED / FALSE — DECIDED
Step: S1. Context: the step names the verdicts `CONFERMATA` / `FALSA`; §2.6 wants everything in the repository in English. Choice: `docs/spike-report.md` uses `CONFIRMED` / `FALSE`, one row per claim in its summary. H8 is a measurement: its verdict only says it was measured.
Reason: §2.6. Visible effect: none.

## N-014 · After an offline `rebuild`, `library/` stays empty until MusicLib renders again — TO CONFIRM
Step: S1 (H7.6). Observed: `.maintenance` exists only while the `rebuild` command runs (35 ms in the spike, `library/` then absent or partial). When the command ends the marker is gone but `library/` is empty, and it fills again only after the app is started and re-renders every album (same `album_id`, `album_revision` and file SHA-256, new `build_id`). §4.5 and A11 cover only the marker. Choice: nothing changes in the design; Vibrance will see an empty library in that interval and mark every album `available = 0`, and the next scans bring everything back with the same ids (non-destructive, §6.4).
Visible effect: between the end of an offline rebuild and MusicLib's re-render, Vibrance shows the albums as unavailable.

## N-015 · Facts the spike established for S4 and S5 — DECIDED
Step: S1. See `docs/spike-report.md`. ffprobe keys: FLAC `TITLE`, `ARTIST`, `album_artist`, `ALBUM`, `track`, `TRACKTOTAL`, `disc`, `DISCTOTAL`, `DATE`, `GENRE`, `COMPILATION`, `REPLAYGAIN_*`; MP3 and M4A lowercase generic keys with the totals inside `track`/`disc` (`2/2`), ReplayGain as `REPLAYGAIN_*` (MP3, TXXX) and `replaygain_*` (M4A). The cover also stays as `Extras/cover.jpg`. An artist rename and a trash/restore each raise `album_revision`; a forced render does not. The hash muxer refuses an MP3 attached picture ("dimensions not set"): do not hash covers with it. The fingerprint costs a SHA-256 of the packets (about 260 ms for 50 MB here).
Reason: recorded once, for the steps that implement them. Visible effect: none.

## N-016 · Where the first two startup steps live — DECIDED
Step: S2. `app.Run(ctx, getenv, euid, cpus, log)` runs steps 1 and 2 of §11.2 and the stop; steps 3–7 and the later stop steps are listed as comments in `startup` and `shutdown`, where their code will go. `cmd/vibrance serve` only sets umask 022, turns SIGTERM and SIGINT into a cancelled context, logs the returned error once with its code, and picks the exit code: 2 for `run_as_root` and `config_invalid`, 1 for anything later. Root is refused before the configuration is read (one refusal, one log line). `syscall.Umask` makes `cmd/vibrance` build on Unix only (§3.5: Linux).
Reason: the step puts steps 1–2 in `internal/app`; signals and the umask belong to the process. Visible effect: none beyond the step.

## N-017 · `http_listen`, and the exit code of a stop that cuts requests — DECIDED
Step: S2. Context: §11.2 names no code for an address that cannot be listened on, and does not say how the process exits when the 10 seconds pass with requests still open. Choice: `http_listen` with exit 1, as in MusicLib, also when the HTTP server stops by itself. A stop that has to close open connections after the grace period logs one `WARN` line and still exits 0 (MusicLib exits 1 there).
Reason: §11.2 describes the forced close as part of the normal stop ("gli stream in corso si interrompono"), and a listener in the middle of a track is the usual case for Vibrance. Visible effect: the exit code of `docker stop` with a stream open is 0.

## N-018 · Health and `/` before the HTTP boundary exists — DECIDED
Step: S2. `/health/ready` answers `503 not_ready` during the startup and `503 shutting_down` once the stop begins (§8.4). The second is hardly ever seen: net/http closes the listener and drops any request it reads after `Shutdown` began, so only a request already in its handler gets it. Bodies are `{"status":…}` or `{code, message, details: {}}` with `Content-Type: application/json`. `GET /` answers `302` to `/api/docs` (not 301: browsers cache it, and `/` is where the web interface will be). Unknown paths and wrong methods still get net/http's plain-text 404 and 405. The headers of §7.6, the JSON 404 and 405, `X-Request-Id` and the access log arrive with the S12 middleware, and `/api/docs` with S20.
Reason: I16; S12 owns the error model and the boundary. Visible effect: until S12, 404 and 405 are plain text and responses lack the §7.6 headers.

## N-019 · Configuration rules the design leaves open — DECIDED
Step: S2. An empty variable is an unset one (Compose passes `${VAR:-}`). `VIBRANCE_HTTP_ADDR` is `host:port` with a decimal port in 1..65535 and an optional host, as in MusicLib. `VIBRANCE_SCAN_INTERVAL` is any value `time.ParseDuration` accepts, at least `30s`, with no upper bound. `VIBRANCE_WORKERS` is a plain decimal integer (no sign, no leading zero). An origin with user information is refused without repeating the value, which may hold a password. `ScanInterval` and `Workers` are validated and logged at startup now; S5 and S8 consume them. The admin variables are not read until S13.
Reason: the most literal reading of §11.1 and MusicLib's rules. Visible effect: none.

## N-020 · `vibrance healthcheck` — DECIDED
Step: S2. It reads only `VIBRANCE_HTTP_ADDR` (an empty or unspecified host becomes the loopback address), does not follow redirects, uses no proxy, and is healthy only for a `200`. A failure writes one `ERROR` line on stdout with code `unhealthy`, or `config_invalid` for an invalid address, and exits 1 in both cases: Docker defines only 0 and 1 for a healthcheck. `vibrance healthcheck extra` is a usage error, exit 2.
Reason: §11.3 and MusicLib's subcommand. Visible effect: none.

## N-021 · How S2 tests the process — DECIDED
Step: S2. The process tests of `cmd/vibrance` build the package once with `go build -race` and run that binary with real signals; a data race in the child shows on its stderr and in its exit code. The uid is a parameter of `run` and `app.Run`, because the gate container runs as an unprivileged user and cannot start a root process: `run_as_root` is tested in-process with uid 0, and was checked by hand with `docker run --user 0:0` on the runtime image. `server.grace` exists so that the in-package test of the forced close takes 300 ms; `TestProcessStopCutsAnOpenRequest` runs the real 10 seconds. The runtime image keeps `CMD ["version"]` until S22, which makes `serve` the default.
Reason: real processes where they matter (§12.1), without privileges the gate does not have. Visible effect: none.

## N-022 · The store's API: `Open`, `WithWriteTx`, `Read`, `Close` — DECIDED
Step: S3. Context: the step names the helpers without their signatures. Choice: `store.Open(ctx, path)` opens the two handles and applies the migrations in one call (nothing may use a database that is not migrated); `WithWriteTx(ctx, fn)` and `Read(ctx, fn)` both run `fn(*Queries)` in a transaction. `Read` is a read transaction, not a bare accessor: §6.3 step 3 asks for one, and several queries of one request must see one state. A panic in `fn` rolls back, or the only write connection would stay locked. The read pool keeps database/sql's defaults (no limit, two idle connections) until S24 measures. `Open` also refuses a database that SQLite could not put in WAL mode, which SQLite reports only as the mode it kept. The tests' own SQL is in the test files.
Reason: the smallest surface that S7 and S15 need. Visible effect: none.

## N-023 · PRAGMA outside `sql/`; what sqlc 1.31.1 and the driver do — DECIDED
Step: S3. sqlc drops a PRAGMA statement without an error and generates nothing, so `PRAGMA journal_mode`, `PRAGMA optimize` and `PRAGMA wal_checkpoint(TRUNCATE)` are constants of `internal/store/store.go`, next to the connection settings of the DSN: they configure the file, they are not queries on data (I7). Verified (T7): sqlc honors the goose annotations (a `-- +goose Down` section is ignored); it does read `CREATE VIRTUAL TABLE ... USING fts5` and would generate models for it, but the full-text migration stays out of `sqlc.yaml` as the design asks, and a test checks the list. sqlc cuts the text of every query at the wrong byte when its file holds a non-ASCII character (the generated SQL loses its end, and `sqlc diff` is content): `sql/*.sql` are ASCII, and a test checks it. Verified (T4): modernc.org/sqlite v1.59.0 accepts `_pragma` and `_txlock`, applies them to every connection, and rolls back by itself when a COMMIT fails.
Reason: the facts the later steps need. Visible effect: none.

## N-024 · Versions of the two new modules — DECIDED
Step: S3. `modernc.org/sqlite v1.59.0` and `github.com/pressly/goose/v3 v3.27.0`, both in §2.4: the newest releases whose `go.mod` says `go 1.25.0` (sqlite 1.60.0 asks for go 1.26, goose 3.27.1 for go 1.25.7). goose 3.27.0 is also MusicLib's. Their indirect requirements are in `go.mod` as `go mod tidy` wrote them; `modernc.org/libc v1.75.7` is the version sqlite 1.59.0 requires, and must move only with it. The SQLite the driver carries is 3.53.4, with FTS5.
Reason: §2.4, "exact versions, compatible with go 1.25.0". Visible effect: none.

## N-025 · What §5.2 leaves open in the schema — DECIDED
Step: S3. Two migrations: `00001_schema.sql` (everything sqlc reads) and `00002_search.sql` (FTS5). Every domain §5.2 writes is a CHECK with a name (`<table>_<column>_check`): the booleans, `role`, `kind`, `codec`, `year`, the username rule of §7.1 (with GLOB: SQLite has no regular expressions), the lengths of `device_name`, `name` and `description`, `occurrence >= 1`, `disc`, `no`, `revision > 0`, `position >= 0`, `cover_rel`, `cover_mime`. `year_key = coalesce(year, 10000)` is a CHECK on a plain column, not a generated column. The formats of ids and hashes have no CHECK: the code that makes them guarantees them. Foreign keys whose action §5.2 does not state (`albums.artist_id`, `tracks.album_id`, `playlist_items.track_id`) are `ON DELETE RESTRICT`, like `favorites.track_id`: those rows are never deleted (I3). Text primary keys are `NOT NULL` explicitly (SQLite does not imply it). `seq` has no AUTOINCREMENT: rows are never deleted, so no rowid is reused. `"key"` and `"no"` are quoted, being SQLite keywords. Only the indexes §5.2 lists exist.
Reason: the schema says what the design says, and nothing the later steps would have to undo. Visible effect: none.

## N-026 · Step 3 of the startup: the state folder and the codes — DECIDED
Step: S3. Context: §11.2 names only `store_schema_too_new`. Choice: `state_unwritable` when the server cannot create and remove the file `.write-check` in the state folder; `store_open`, `store_migrate` and, at the stop, `store_close` from the store. All exit 1, like every failure after step 1 (N-016). The state folder is a parameter of `app.Run`. In `cmd/vibrance` it is `var stateDir = "/var/lib/vibrance"`: nothing at runtime can change it (§3.4), and the process tests link the real binary with `-ldflags "-X main.stateDir=<temporary folder>"`, so that they run on ext4 and not on the read-only root of the gate container. The database file has mode 0644, from the umask 022 of S2. The log has two new events, `database open` and `database closed`.
Reason: the fixed path stays fixed, and the tests still run the real program. Visible effect: the new codes in the log.

## N-027 · Two exit codes around the database — TO CONFIRM
Step: S3. Context: §11.2 does not say how the process ends in two cases. (1) A stop (SIGTERM) that arrives while the startup is still opening or migrating the database interrupts that step: the server stops like any other time and exits 0, and the `stopping` event carries what was interrupted. (2) At the stop, if another process still reads the database (a subcommand in the middle of a query), the final `wal_checkpoint(TRUNCATE)` waits the busy timeout (5 s) and then cannot empty the log: the server logs `store_close` and exits 1. Nothing is lost: the log is recovered at the next start.
Reason: (1) a stop is not a failure of the step it interrupts; (2) no error of the closing is passed over (I9), and the alternative is a silent difference between a clean and an unclean stop. Visible effect: the exit code of `docker stop` in those two cases.

## N-028 · Facts about SQLite for S13 and S21 — DECIDED
Step: S3. `VACUUM INTO` is refused on a `query_only` connection (`SQLITE_READONLY`), although it writes another file: the backup needs a connection that may write. It keeps every rowid of `artists`, `albums` and `tracks` because `seq` is the declared key, and the FTS5 tables with them (tested). `PRAGMA optimize` at the stop may create `sqlite_stat1`. goose's version table makes `sqlite_sequence` exist. Two processes that both find an empty database and migrate it at the same moment are not coordinated: one fails with `store_migrate`, nothing half-applied (a migration is one transaction), and succeeds when started again. Today only the server migrates; a subcommand that opens the database while the server runs finds it migrated, and the steps that add subcommands decide whether they call `store.Open`. A foreign key with `ON DELETE RESTRICT` fails with `SQLITE_CONSTRAINT_TRIGGER` (1811), not `SQLITE_CONSTRAINT_FOREIGNKEY` (787).
Reason: recorded once, for the steps that meet them. Visible effect: none.

## N-029 · What a transaction returns when its context ends — DECIDED
Step: S3. Context: the design does not say it, and the libraries answer by timing (all measured). When the context ends, database/sql (Go 1.25.14) rolls the transaction back from a goroutine of its own: a `Commit`, or a statement of `fn`, that meets it returns the context's error or `sql.ErrTxDone`, whichever came first. With modernc.org/sqlite v1.59.0 a `BEGIN` that the end of the context interrupts fails with `SQLITE_INTERRUPT`, on both handles; one that waits for the write lock of another process is not cut short, and fails with `SQLITE_BUSY` after the 5 s of `busy_timeout`. Choice: once the context is over, every error of `WithWriteTx` and `Read`, of `fn` too, wraps the context's error and then its cause (`errors.Is` finds both); while the context lasts the error of `fn` is returned as it is. An error means that nothing was committed, and no error that the commit is done, also when the context ended during it. `Read` returns no error when `fn` returns none.
Reason: one rule, so that a caller tells a cancelled request from a failure of the database with `errors.Is(err, context.Canceled)`, and no cause is hidden. Visible effect: none.
