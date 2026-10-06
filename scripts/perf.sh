#!/usr/bin/env bash
# The performance suite (DESIGN.md §14, step S24), in Docker: it generates
# the synthetic dataset the budgets are stated for, starts the real program
# on it and fails when a budget is missed.
#
# Usage: scripts/perf.sh [go-test-run-pattern]     default: ^TestPerf
#   scripts/perf.sh                  the whole suite, about 17 minutes
#   scripts/perf.sh TestPerfScan     one test
#
# It prints one line `PERF | measure | n | p50 | p95 | max | budget | verdict`
# for every measure, and the plan of every query of the store (`PLAN ...`).
# A measure without a budget is only reported.
#
# It runs the tests of internal/perfgen and the memory test of
# internal/covers, which carry the build tag `perf`, in the `dev` container:
# on the live sources, without the race detector, with the dataset in TMPDIR
# on the ext4 test volume of the project vibrance-dev, which is emptied when
# the run ends. It is not part of the gate: it takes minutes, and its
# figures mean something only on a machine that is doing nothing else. A
# budget missed while something else loaded the machine (a game, a build,
# another container) is not a regression: run the suite again at rest
# before reading anything into it.
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"

[[ $# -le 1 ]] || die "usage: scripts/perf.sh [go-test-run-pattern]"
pattern="${1:-^TestPerf}"

info "building the dev image (uid ${VIBRANCE_DEV_UID}:${VIBRANCE_DEV_GID})"
compose_dev build dev || die "building the dev image failed"

# -p 1: one package at a time, so that no test measures while another loads
# the machine.
info "running the performance suite (${pattern})"
compose_dev --profile tools run --rm dev \
  go test -tags perf -count=1 -p 1 -v -timeout 90m -run "${pattern}" ./internal/perfgen ./internal/covers
