#!/usr/bin/env bash
# Checks that compose.yaml and env.example are MusicLib's own files plus
# Vibrance's declared additions (DESIGN.md §11.7, D15). The release gate
# runs it.
#
# Usage: scripts/check-compose-sync.sh [DIR]
#   DIR holds the compose.yaml and env.example to check (default: the
#   repository). scripts/stack-smoke.sh passes a copy it has altered.
#
# Each file may hold blocks that begin with a line `# >>> Vibrance` and end
# with the next line `# <<< Vibrance` (leading spaces allowed): Vibrance's
# additions. Two checks, against the files of the MusicLib release whose
# image the Dockerfile pins (ARG MUSICLIB_IMAGE, the one place that names the
# version):
#   1. with the blocks removed, each file is MusicLib's, byte for byte;
#   2. Compose's model of compose.yaml, without the service `vibrance` and the
#      volumes `vibrance_state` and `vibrance_backup`, is the model of
#      MusicLib's file: a block cannot change a service of MusicLib either.
# It needs network access, to download MusicLib's release files.
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"

[[ $# -le 1 ]] || die "usage: scripts/check-compose-sync.sh [DIR]"
dir="${1:-${REPO_ROOT}}"
[[ -f "${dir}/compose.yaml" && -f "${dir}/env.example" ]] \
  || die "${dir} must hold compose.yaml and env.example"

# The pinned MusicLib image, ghcr.io/tommasonovelli/musiclib:X.Y.Z@sha256:...
musiclib_image="$(sed -n 's/^ARG MUSICLIB_IMAGE=//p' "${REPO_ROOT}/Dockerfile")"
version="$(sed -n 's|^ghcr\.io/tommasonovelli/musiclib:\([0-9][0-9.]*\)@sha256:[0-9a-f]\{64\}$|\1|p' <<<"${musiclib_image}")"
[[ -n "${version}" ]] || die "cannot read the MusicLib version from ARG MUSICLIB_IMAGE of the Dockerfile"
# curl comes from the pinned Go image of the toolchain: the host needs only
# Docker.
go_image="$(sed -n 's/^ARG GO_IMAGE=//p' "${REPO_ROOT}/Dockerfile")"
[[ "${go_image}" == *@sha256:* ]] || die "cannot read ARG GO_IMAGE from the Dockerfile"
readonly release="https://github.com/tommasonovelli/vibrance-musiclib/releases/download/v${version}"

work="$(mktemp -d)"
trap 'rm -rf -- "${work}"' EXIT

download() {
  docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges:true \
    --user 65534:65534 "${go_image}" \
    curl -fsSL --proto '=https' --max-time 60 "${release}/$1" >"$2" \
    || die "cannot download ${release}/$1"
  [[ -s "$2" ]] || die "${release}/$1 is empty"
}

# Prints the file without the blocks of Vibrance; fails on a block that is
# not closed, an end without a beginning, or a block inside a block.
strip_blocks() {
  awk -v file="$1" '
    /^[[:space:]]*# >>> Vibrance$/ {
      if (inside) { printf "%s:%d: block inside a block\n", file, NR > "/dev/stderr"; bad = 1 }
      inside = 1; next
    }
    /^[[:space:]]*# <<< Vibrance$/ {
      if (!inside) { printf "%s:%d: end of a block that did not begin\n", file, NR > "/dev/stderr"; bad = 1 }
      inside = 0; next
    }
    !inside { print }
    END {
      if (inside) { printf "%s: a block does not end\n", file > "/dev/stderr"; bad = 1 }
      exit bad
    }' "$1"
}

# Prints Compose's model of a Compose file, without the entries of Vibrance:
# the service `vibrance` and the two volumes. The model is interpolated with
# an empty environment, so every variable takes its default: older Compose v2
# releases (v2.33, on GitHub's runners) cannot read the short volume syntax
# `${VAR:-x}:/path` without interpolating it. Nothing is created: `config`
# only reads the files.
model() {
  docker compose --project-name vibrance-dev --project-directory "${work}" \
    --env-file "${work}/empty.env" -f "$1" config |
    awk '
      /^[^ ]/ { section = $0; skip = 0 }
      /^  [^ ]/ {
        skip = (section == "services:" && $0 == "  vibrance:") ||
          (section == "volumes:" && ($0 == "  vibrance_state:" || $0 == "  vibrance_backup:"))
      }
      !skip { print }'
}

# docker.exe on Windows needs the native form of the paths (DESIGN.md T22).
case "${OSTYPE:-}" in
  msys* | cygwin*)
    work="$(cd -- "${work}" && pwd -W)"
    dir="$(cd -- "${dir}" && pwd -W)"
    ;;
esac
: >"${work}/empty.env"

info "downloading compose.yaml and env.example of MusicLib ${version}"
download compose.yaml "${work}/musiclib-compose.yaml"
download env.example "${work}/musiclib-env.example"

failed=0
for pair in "compose.yaml musiclib-compose.yaml" "env.example musiclib-env.example"; do
  read -r ours theirs <<<"${pair}"
  if ! strip_blocks "${dir}/${ours}" >"${work}/stripped-${ours}"; then
    printf '%s: %s: the blocks of Vibrance are not well formed\n' "${0##*/}" "${ours}" >&2
    failed=1
  elif ! diff -u "${work}/${theirs}" "${work}/stripped-${ours}" >&2; then
    printf '%s: %s: outside the blocks of Vibrance it differs from MusicLib %s (above: - MusicLib, + this file)\n' \
      "${0##*/}" "${ours}" "${version}" >&2
    failed=1
  fi
done

model "${work}/musiclib-compose.yaml" >"${work}/model-musiclib" || die "docker compose cannot read MusicLib's compose.yaml"
model "${dir}/compose.yaml" >"${work}/model-ours" || die "docker compose cannot read compose.yaml"
if ! diff -u "${work}/model-musiclib" "${work}/model-ours" >&2; then
  printf '%s: compose.yaml: the blocks of Vibrance change what MusicLib %s defines (above: - MusicLib, + this file)\n' \
    "${0##*/}" "${version}" >&2
  failed=1
fi

[[ "${failed}" == 0 ]] || die "compose.yaml or env.example is not MusicLib ${version} plus Vibrance's blocks"
info "compose.yaml and env.example are MusicLib ${version} plus Vibrance's blocks"
