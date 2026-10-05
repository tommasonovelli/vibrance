#!/usr/bin/env bash
# govulncheck on the whole module, in the toolchain container: the known
# vulnerabilities of the Go standard library and of the modules that
# Vibrance's code reaches. A release check (docs/development.md,
# "Releasing"), not part of the gate: it needs network access, for the tool
# and for the vulnerability database of the day.
#
# Usage: scripts/vulncheck.sh [govulncheck arguments...]     default: ./...
#   scripts/vulncheck.sh                    the code the binary and the tests reach
#   scripts/vulncheck.sh -show verbose ./...   also what is imported but not reached
#
# The tool is pinned to an exact version and run with `go run pkg@version`,
# which builds it outside the module: nothing is added to go.mod. The build
# tags of the contract and performance suites are included. It exits 0 when
# no reachable vulnerability is found, 3 when one is.
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"

readonly GOVULNCHECK='golang.org/x/vuln/cmd/govulncheck@v1.7.0'

compose_dev build dev || die "building the dev image failed"

[[ $# -gt 0 ]] || set -- ./...
compose_dev --profile tools run --rm dev go run "${GOVULNCHECK}" -tags contract,perf "$@"
