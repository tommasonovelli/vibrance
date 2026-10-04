#!/usr/bin/env bash
# Regenerates internal/api/api.gen.go from api/openapi.yaml with oapi-codegen
# (api/oapi-codegen.yaml), the version go.mod pins as a `tool` directive.
# Commit the generated file together with the specification:
# scripts/check.sh fails while it is out of date.
#
# Usage: scripts/generate.sh            generate, on the live sources
#        scripts/generate.sh diff       only report differences, on a snapshot
#                                       of the tree and without network
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"

case "${1:-generate}" in
  generate)
    compose_dev build dev || die "building the dev image failed"
    compose_dev --profile tools run --rm dev /src/docker/generate.sh generate
    ;;
  diff)
    compose_dev build test || die "building the test image failed"
    compose_dev --profile tools run --rm test /src/docker/generate.sh diff
    ;;
  *) die "usage: scripts/generate.sh [generate|diff]" ;;
esac
