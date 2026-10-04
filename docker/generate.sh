#!/usr/bin/env bash
# Generates internal/api/api.gen.go from api/openapi.yaml with oapi-codegen,
# the version go.mod pins as a `tool` directive, or checks that the committed
# file is what the generator writes.
#
# Runs INSIDE the container, from the module root, without network: the tool
# is built from the module cache of the image.
#
# Usage: generate.sh generate     write internal/api/api.gen.go
#        generate.sh diff         fail if it differs from what would be written
set -euo pipefail

readonly CONFIG=api/oapi-codegen.yaml
readonly SPEC=api/openapi.yaml
readonly GENERATED=internal/api/api.gen.go

die() {
  printf 'generate: error: %s\n' "$*" >&2
  exit 1
}

readonly MODE="${1:-}"
[[ "${MODE}" == generate || "${MODE}" == diff ]] || die "usage: generate.sh generate|diff"
[[ -f go.mod ]] || die "go.mod not found in $(pwd): run from the module root"

# The generator prints the code; it is kept in a file of TMPDIR (the test
# volume) until it is whole, so a failure never leaves half a file in the
# sources, and `diff` never writes there (they are read-only in the gate).
fresh="$(mktemp "${TMPDIR:-/tmp}/api.gen.XXXXXXXX")"
trap 'rm -f -- "${fresh}"' EXIT
go tool oapi-codegen -config "${CONFIG}" "${SPEC}" >"${fresh}" \
  || die "oapi-codegen failed on ${SPEC}"
[[ -s "${fresh}" ]] || die "oapi-codegen wrote nothing"

case "${MODE}" in
  generate)
    cat -- "${fresh}" >"${GENERATED}"
    ;;
  diff)
    diff -u -- "${GENERATED}" "${fresh}" \
      || die "${GENERATED} is out of date with ${SPEC}: run scripts/generate.sh"
    ;;
esac
