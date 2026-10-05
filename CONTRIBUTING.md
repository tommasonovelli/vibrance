# Contributing to Vibrance

Read the [README](README.md) and the rules and invariants in [AGENTS.md](AGENTS.md) before changing behavior; the design they summarize is [DESIGN.md](DESIGN.md), in Italian. Contributions of original code and documentation must be available under the project's [MIT License](LICENSE). Include the origin and license of any third-party material, keep its notices ([THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)), and ask the maintainer before adding a dependency: the list of allowed Go modules is short on purpose.

## Development setup

Use Docker with the Compose v2 plugin: the pinned toolchain is in the repository, and nothing else is installed on the host. On Windows, run the scripts from Git Bash and keep the checkout's LF line endings. The [developer guide](docs/development.md) describes every script and how the server works.

```sh
scripts/dev.sh                  # a shell in the toolchain container, on the live sources
scripts/check.sh                # the gate: generated code up to date, build, vet, gofmt, race tests
scripts/lint-shell.sh           # after changing a shell script
scripts/sqlc.sh                 # after changing sql/ or migrations/
scripts/generate.sh             # after changing api/openapi.yaml
```

The scripts use only the Compose projects `vibrance-dev`, `vibrance-spike` and `vibrance-contract`, never `musiclib`, which is a real installation.

## Preparing a change

Keep a change small and say what problem it solves, what the behavior is afterwards and how you checked it. The API changes in `api/openapi.yaml` first, then in the code; SQL lives in `sql/`; the generated code is committed. Tests use the real things (SQLite on ext4, the pinned `ffmpeg`, real processes) and cover the error paths; when a change touches a guarantee, break it on purpose once and check that a test fails. Documentation changes together with behavior. Use the fixture library of `testdata/`, never personal music or artwork.

Before proposing a change, run `scripts/check.sh` on the whole module, and `scripts/contract.sh` when it touches how the library is read or how tracks keep their identity. Passing on Docker Desktop does not replace the [checks before a release](docs/development.md#before-a-release) on native Linux.

## Reports and releases

For a bug report, include the version, the host and its Docker and Compose versions, the relevant settings without passwords, the steps, what you expected and what happened, and the log lines with their `code`. Never attach `.env`, a database, cookies, tokens or private music. **Security problems are reported privately**: see [SECURITY.md](SECURITY.md).

A release is published by pushing a version tag; the maintainer's procedure is in [Releasing](docs/development.md#releasing). Changes are recorded in [CHANGELOG.md](CHANGELOG.md), in the Keep a Changelog style: one `## [X.Y.Z] - YYYY-MM-DD` section per release, which the release workflow requires and publishes as the release notes.
