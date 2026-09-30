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
