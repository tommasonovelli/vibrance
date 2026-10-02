#!/usr/bin/env bash
# Regenerates the fixture library testdata/library-v1/ and testdata/FIXTURE.md
# with the real MusicLib (DESIGN.md §12.2, step S1).
#
# Usage: scripts/make-fixture-library.sh
#
# It starts the published MusicLib 1.2.0 in the Compose project
# `vibrance-spike` (new volumes, random passwords, no published port),
# creates the input albums A–F, imports them through MusicLib's API, copies
# library/ into testdata/library-v1/ and writes testdata/FIXTURE.md; then it
# deletes the whole project. It needs network access (images, the Debian
# snapshot for LAME). Every run gives new album_id values.
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"
# shellcheck source=scripts/lib/spike.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/spike.sh"

[[ $# -eq 0 ]] || die "usage: scripts/make-fixture-library.sh (no arguments)"

spike_begin
spike_inputs
spike_import
spike_tool fixture || die "copying the library failed"
info "done: testdata/library-v1/ and testdata/FIXTURE.md"
