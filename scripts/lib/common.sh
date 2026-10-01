# shellcheck shell=bash
# Shared helpers for the host-side scripts. Source it; do not execute it.
#
# Sets:    REPO_ROOT, and exports VIBRANCE_DEV_UID / VIBRANCE_DEV_GID for Compose.
# Defines: die, info, compose_dev, run_sqlc.

die() {
  printf '%s: error: %s\n' "${0##*/}" "$*" >&2
  exit 1
}

info() {
  printf '%s: %s\n' "${0##*/}" "$*" >&2
}

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
# Git Bash (MSYS) on Windows rewrites every argument that looks like a POSIX
# path before it reaches docker.exe (`--workdir /src` becomes
# `C:/Program Files/Git/src`). Turn that off, and give Docker the native
# Windows form of the repository path instead (DESIGN.md §3.6, T22).
case "${OSTYPE:-}" in
  msys* | cygwin*)
    export MSYS_NO_PATHCONV=1
    REPO_ROOT="$(cd -- "${REPO_ROOT}" && pwd -W)"
    ;;
esac
readonly REPO_ROOT

command -v docker >/dev/null 2>&1 \
  || die "docker not found in PATH: Docker is the only prerequisite (see README.md)"
docker compose version >/dev/null 2>&1 \
  || die "'docker compose' (Compose v2 plugin) is not available"
docker info >/dev/null 2>&1 \
  || die "cannot reach the Docker daemon (is it running? is your user allowed to use it?)"

# The toolchain containers run as the host user, so that files written into
# the bind-mounted sources (dev) belong to you. Root is refused as the test
# user: root bypasses permission checks and would hide bugs.
if [[ -z "${VIBRANCE_DEV_UID:-}" ]]; then
  VIBRANCE_DEV_UID="$(id -u)"
  VIBRANCE_DEV_GID="$(id -g)"
  if [[ "${VIBRANCE_DEV_UID}" == 0 ]]; then
    VIBRANCE_DEV_UID=10001
    VIBRANCE_DEV_GID=10001
  fi
fi
[[ "${VIBRANCE_DEV_UID}" != 0 ]] || die "VIBRANCE_DEV_UID=0: the tests must not run as root"
export VIBRANCE_DEV_UID VIBRANCE_DEV_GID="${VIBRANCE_DEV_GID:-${VIBRANCE_DEV_UID}}"

# The development Compose project (DESIGN.md §3.6). Never `musiclib`: that is
# the user's installation.
readonly DEV_PROJECT=vibrance-dev

# docker compose on this repository's development file, compose.dev.yaml,
# always as the project vibrance-dev, whatever the cwd, COMPOSE_FILE or
# COMPOSE_PROJECT_NAME say.
compose_dev() {
  docker compose --project-name "${DEV_PROJECT}" --project-directory "${REPO_ROOT}" \
    -f "${REPO_ROOT}/compose.dev.yaml" "$@"
}

# sqlc (DESIGN.md §3.6), pinned like every other image (I12), the same pin as
# MusicLib.
readonly SQLC_IMAGE='sqlc/sqlc:1.31.1@sha256:70f53171d27b2424e9358869975455a6e955a5aa8e58a998a270a6e34e525537'

# run_sqlc <ro|rw> <sqlc args...>: sqlc on the repository, as the host user,
# without network. `ro` mounts the sources read-only (diff); `rw` lets
# `generate` write internal/store.
run_sqlc() {
  local mode="$1"
  shift
  local mount="type=bind,source=${REPO_ROOT},target=/src"
  [[ "${mode}" == ro ]] && mount+=",readonly"
  docker run --rm --network none --read-only --tmpfs /tmp --env HOME=/tmp \
    --user "${VIBRANCE_DEV_UID}:${VIBRANCE_DEV_GID}" --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --mount "${mount}" --workdir /src "${SQLC_IMAGE}" "$@"
}
