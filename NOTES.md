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
