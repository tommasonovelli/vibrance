# Vibrance: rules for contributors and agents

These rules apply to anyone who changes this repository, human or AI agent. `AGENTS.md` and `CLAUDE.md` have identical content, so change both together. The normative specification is [DESIGN.md](DESIGN.md), written in Italian. When this file and DESIGN.md disagree, DESIGN.md wins.

## What Vibrance is

Vibrance is a read-only listening server over the `library/` folder that Vibrance MusicLib produces. It has multiple users (`admin`, `user`), favorites, private playlists, full-text search, and an HTTP API described in OpenAPI and served under `/api/v1`. It is one Go binary (`vibrance`) with SQLite (`modernc.org/sqlite`), and it runs the pinned `ffmpeg`/`ffprobe` as child processes. It ships as a Docker image. **Few features, done well:** simplicity is a permanent requirement.

## How the work is organized

- DESIGN.md §14 is the plan, one step at a time (S0…S25). An orchestrator runs each step through an engineer and then a peer reviewer. `PROGRESS.md` records the state of every step.
- **The engineer never commits** and never edits `DESIGN.md` or `PROGRESS.md`. Those belong to the orchestrator, who commits only after the reviewer approves.
- The engineer implements the whole step, and only the step (I16).
- Record decisions, deviations, risks and questions in [NOTES.md](NOTES.md), one short entry per topic: `DECIDED` when the design is silent or ambiguous without visible effect, `TO CONFIRM` when the ambiguity changes visible behavior. Pick the most conservative option and go on.
- If the design contradicts itself or rests on a false assumption, stop and report a `BLOCCO:` with evidence (commands and output). Never work around it.
- Never modify `.ref/`, the read-only clone of MusicLib 1.2.0 kept for reference. It is never committed.
- Releases are made only by the owner, by pushing a tag `vX.Y.Z` ([docs/development.md](docs/development.md), "Releasing"). Agents never create tags and never publish. The orchestrator pushes `main` only with the owner's consent.
- `web/` holds the documentation page built into the binary, with the vendored Scalar script (`web/docs/VENDOR.md`). `THIRD_PARTY_NOTICES.md` and `licenses/` change together with every Go module or vendored file.

## Build, test, run: Docker only

- The host needs only Docker with the Compose v2 plugin. Never install Go, ffmpeg or other tools on the host. On Windows, use Git Bash (the scripts set `MSYS_NO_PATHCONV=1`) and keep LF line endings (`.gitattributes`).
- `scripts/check.sh [packages]` is the gate. It checks that the generated code is up to date (`sqlc diff` for `internal/store`, `generate.sh diff` for `internal/api`), then runs `go build`, `go vet` (also with the build tags `contract` and `perf`), `gofmt` and `go test -race` on the whole module by default, in a container without network, with `TMPDIR` on a real ext4 volume. It tests a snapshot of the tree taken at build time. It must pass on the whole module before a step is done.
- `scripts/dev.sh [cmd]` gives a shell, or runs one command, in the toolchain container on the live sources. For example, repeat concurrency tests with `scripts/dev.sh go test -race -count=20 -run TestX ./internal/...`.
- `scripts/sqlc.sh` regenerates `internal/store` after any change to `sql/` or `migrations/`. Commit the result. `sqlc.yaml` lists the migrations one by one, without the full-text one. Keep `sql/*.sql` in ASCII: sqlc cuts the queries of a file with other characters at the wrong byte.
- `scripts/generate.sh` regenerates `internal/api/api.gen.go` after any change to `api/openapi.yaml` or `api/oapi-codegen.yaml`. Commit the result. `oapi-codegen` is pinned as a `tool` directive of `go.mod`. Never edit the generated file by hand.
- `scripts/lint-shell.sh` runs shellcheck, after any change to a shell script.
- Outside the gate, because they need the network or a quiet machine: `scripts/check-compose-sync.sh` (the stack files against MusicLib's release), `scripts/stack-smoke.sh` and `scripts/contract.sh` (project `vibrance-contract`, no published port; the contract suite is required before a release and after a change of MusicLib's version), `scripts/perf.sh` (about 25 minutes, on an idle machine, project `vibrance-dev`) and `scripts/vulncheck.sh` (pinned `govulncheck`, before a release).
- The only Compose projects to use are `vibrance-dev` (development), `vibrance-spike` (manual trials) and `vibrance-contract` (contract suite). **Never** use `musiclib`, the user's installation. Never run `docker compose down -v` or `docker volume rm` on anything else.

## Principles and decisions (DESIGN.md §2.1–§2.2)

- Simple, boring, verifiable. The only two novelties are track identity by audio fingerprint and the API contract as the source of code and tests.
- The only link to MusicLib is the `library/` folder, read-only: no API, no shared database, no startup dependency. Vibrance works fully with MusicLib stopped.
- The scanner uses level-triggered reconciliation: it is idempotent, non-destructive and commits one transaction per album. The software is crash-only: stopping is exiting, and starting is recovering.
- Measure before optimizing: no cache, pool or parallelism without a measurement.
- Pins: the same toolchain pins as MusicLib (Go 1.25.14, Debian trixie, images by digest). `ffmpeg`/`ffprobe` `8.1.3-musiclib1` are copied from `ghcr.io/tommasonovelli/musiclib:1.2.0`.
- SQLite with `modernc.org/sqlite`, with no Redis, no PostgreSQL and no search service. Search uses FTS5. OpenAPI 3.0.3 is written by hand before the code, with `oapi-codegen` (strict server) and `kin-openapi`.
- A track id is a UUID kept through the audio fingerprint, never through a path or a number.
- Errors are `{code, message, details}` with stable codes. Pagination is `?limit=&after=`. JSON is strict: unknown or duplicate keys are rejected.
- Timestamps are integer Unix milliseconds (UTC). Ids are lowercase UUID `TEXT`.

## Invariants (DESIGN.md §2.3, non-negotiable)

- **I1** Vibrance never writes to `/musiclib`. It reads only `library/` and the `.maintenance` and `.musiclib-store` markers, always through `os.Root`.
- **I2** Client input never forms a filesystem path. Paths come only from the `rel_path` columns that the scanner writes.
- **I3** The scanner never deletes rows of `artists`, `albums` or `tracks`. It only changes `available`. A track id never changes.
- **I4** Every request other than GET and HEAD needs `X-Vibrance-Request: 1`. `Host` must match `VIBRANCE_PUBLIC_ORIGIN` (421), and `Origin`, if present, must equal it (403). No CORS. GET and HEAD have no visible side effects.
- **I5** Passwords, tokens, cookies and hashes never appear in logs, errors, responses or test messages. Tokens are stored only as SHA-256, and passwords only as PHC argon2id. Comparisons run in constant time.
- **I6** Every endpoint is in the authorization matrix of the tests. Another user's resource answers `404`, never `403`.
- **I7** All SQL lives in `sql/*.sql` (sqlc). The only exceptions are FTS5 in `internal/search` and the constant `PRAGMA` and `VACUUM INTO` statements of `internal/store`, which sqlc drops. No SQL string is built from user input.
- **I8** No external process runs without a `context`, a timeout, a stderr limit, and files passed by descriptor (never by path).
- **I9** No error is ignored, especially those of `Close`, `Rollback`, `Commit` and `Rows.Err`.
- **I10** Every API response conforms to the OpenAPI specification, as the tests verify with `kin-openapi`.
- **I11** There is one SQLite write connection. Write transactions are short, with no I/O (ffmpeg, disk, network) inside them.
- **I12** Everything is pinned: images by digest, modules at exact versions, never `latest`.
- **I13** Migrations only go forward. Once released they are never edited, and a database newer than the binary is refused. The first release is 0.1.0: from it on, the existing migrations are closed and every change is a new migration.
- **I14** Vibrance works without MusicLib and without `library/`.
- **I15** Only the scanner changes the `available` columns.
- **I16** No function, dependency, option or step that the design does not ask for.

## Allowed Go dependencies (DESIGN.md §2.4)

`modernc.org/sqlite`, `github.com/pressly/goose/v3`, `github.com/google/uuid`, `golang.org/x/crypto`, `golang.org/x/text`, `golang.org/x/image`, `golang.org/x/sync`, `github.com/getkin/kin-openapi`, `github.com/oapi-codegen/runtime`, `github.com/oapi-codegen/nethttp-middleware`, `github.com/google/go-cmp` (tests only), and `oapi-codegen` as a `tool` directive. Use exact versions, compatible with `go 1.25.0`. Any other dependency needs a `TO CONFIRM` entry in NOTES.md.

## Code style (DESIGN.md §2.5)

- Write small functions with explicit inputs and outputs, and use concrete types. Add interfaces only at boundaries that the tests need. Pass dependencies in constructors.
- No ORM, DI container, event bus, job framework, external queue, speculative cache or speculative configuration.
- Errors are typed, with stable codes and context added along the chain (`fmt.Errorf("...: %w", err)`). Every long operation takes a `context.Context`.
- The reconciliation planner does no I/O. The store knows no absolute paths. The media adapter knows nothing of the domain. HTTP handlers contain no SQL. `app` is the only package that wires everything together.
- There is one implementation each of name normalization, sort keys, cursor encoding and path validation.
- Comments explain only the *why*. Documentation, the OpenAPI specification and tests change together with any change of a guarantee. Generated code (sqlc, oapi-codegen) is committed.
- Tests use the real thing where it matters: real SQLite on ext4, the real pinned ffmpeg, real processes. Nothing mocks SQL, the filesystem or processes. Cover error paths and concurrency. Break important invariants on purpose and check that a test fails (mutation checks). Fuzz targets run for at least 60 seconds when introduced.

## Language (DESIGN.md §2.6)

Everything in the repository is in English: code, identifiers, comments, error messages, logs, tests, documentation and commits. Only DESIGN.md is in Italian. Comments may cite `DESIGN.md §x`, but important rules are written out in full.
