#!/usr/bin/env bash
# The quality gate, in Docker: generated code up to date (sqlc for
# internal/store, oapi-codegen for internal/api), then go build, go vet,
# gofmt, go test -race.
#
# Usage: scripts/check.sh [package-pattern...]     default: ./...
#   scripts/check.sh                          everything
#   scripts/check.sh ./internal/media/...     one package tree
#
# The sources are copied into the `test` image (a snapshot of the working
# tree at build time); the container has a read-only root filesystem, TMPDIR
# on the ext4 `testdata` volume and no network. See docs/development.md.
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"

info "sqlc diff: generated code in internal/store is up to date"
run_sqlc ro diff || die "internal/store is out of date with sql/ or the migrations: run scripts/sqlc.sh"

info "building the test image (uid ${VIBRANCE_DEV_UID}:${VIBRANCE_DEV_GID})"
compose_dev build test || die "building the test image failed"

info "generate diff: generated code in internal/api is up to date"
compose_dev --profile tools run --rm test /src/docker/generate.sh diff \
  || die "internal/api is out of date with api/openapi.yaml: run scripts/generate.sh"

info "running the gate"
compose_dev --profile tools run --rm test /src/docker/gate.sh "$@"
