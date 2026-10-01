#!/usr/bin/env bash
# Runs one Go fuzz target in Docker, on the live sources.
#
# Usage: scripts/fuzz.sh <FuzzTarget> <duration> <package>
#   scripts/fuzz.sh FuzzParseReceipt 60s ./internal/library
#   scripts/fuzz.sh FuzzClassify 10m ./internal/library
#
# The sources are bind-mounted, so a failing input is written to
# <package>/testdata/fuzz/<FuzzTarget>/ in your working tree, ready to be
# committed as a regression test (DESIGN.md §12.5). The fuzzing cache lives
# in the go-build-cache volume and survives across runs.
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"

usage="usage: scripts/fuzz.sh <FuzzTarget> <duration> <package>  (e.g. FuzzParseReceipt 60s ./internal/library)"
[[ $# -eq 3 ]] || die "${usage}"

target="$1"
duration="$2"
pkg="$3"

[[ "${target}" =~ ^Fuzz[A-Za-z0-9_]*$ ]] || die "invalid fuzz target '${target}': ${usage}"
[[ "${duration}" =~ ^[0-9]+(ns|us|ms|s|m|h|x)$ ]] \
  || die "invalid duration '${duration}': use a Go duration (e.g. 60s, 10m) or a count (e.g. 1000x)"
[[ "${pkg}" =~ ^\./[A-Za-z0-9_./-]+$ && "${pkg}" != *...* ]] \
  || die "invalid package '${pkg}': a single ./relative/dir, no '...'"
[[ -d "${REPO_ROOT}/${pkg#./}" ]] || die "no such package directory: ${pkg}"

info "building the dev image (uid ${VIBRANCE_DEV_UID}:${VIBRANCE_DEV_GID})"
compose_dev build dev || die "building the dev image failed"

info "fuzzing ${target} in ${pkg} for ${duration}"
compose_dev --profile tools run --rm dev \
  go test "${pkg}" -run='^$' -fuzz="^${target}\$" -fuzztime="${duration}"
