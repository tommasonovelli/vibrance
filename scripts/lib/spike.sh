# shellcheck shell=bash
# Helpers of the scripts that run the real MusicLib (make-fixture-library.sh,
# spike.sh). Source it after common.sh; do not execute it.
#
# Defines: compose_spike, spike_begin, spike_tool, spike_put, spike_inputs,
#          spike_import, and MUSICLIB_IMAGE (the Dockerfile's pin).
#
# Everything happens in the Compose project `vibrance-spike` (DESIGN.md
# §3.6), with scripts/spike/compose.yaml. spike_begin deletes any leftover of
# that project, starts it with new volumes, and arranges for the whole
# project, volumes included, to be deleted when the script exits. No other
# project is ever touched.

readonly SPIKE_PROJECT=vibrance-spike

# The pins, read from the Dockerfile so that they are written in one place.
MUSICLIB_IMAGE="$(sed -n 's/^ARG MUSICLIB_IMAGE=//p' "${REPO_ROOT}/Dockerfile")"
DEBIAN_IMAGE="$(sed -n 's/^ARG RUNTIME_IMAGE=//p' "${REPO_ROOT}/Dockerfile")"
[[ "${MUSICLIB_IMAGE}" == *@sha256:* && "${DEBIAN_IMAGE}" == *@sha256:* ]] \
  || die "cannot read the pinned MusicLib and Debian images from the Dockerfile"
readonly MUSICLIB_IMAGE DEBIAN_IMAGE

SPIKE_TMP=""
SPIKE_ENV=""

# The host path of a file in the form docker.exe understands (Git Bash
# turns /tmp/... into a Windows path only for native programs it knows).
native_path() {
  case "${OSTYPE:-}" in
    msys* | cygwin*) cygpath -w "$1" ;;
    *) printf '%s\n' "$1" ;;
  esac
}

# docker compose on the spike project, whatever the cwd, COMPOSE_FILE or
# COMPOSE_PROJECT_NAME say, with the run's .env.
compose_spike() {
  docker compose --project-name "${SPIKE_PROJECT}" --project-directory "${REPO_ROOT}" \
    -f "${REPO_ROOT}/scripts/spike/compose.yaml" --env-file "${SPIKE_ENV}" --profile tools "$@"
}

# 64 hex digits from /dev/urandom.
random_hex() {
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

spike_end() {
  local rc=$?
  if [[ -n "${SPIKE_ENV}" ]]; then
    info "deleting the Compose project ${SPIKE_PROJECT} (containers and volumes)"
    compose_spike down --volumes --remove-orphans --timeout 30 >/dev/null 2>&1 \
      || printf '%s: warning: could not delete the project %s; run: docker compose -p %s down -v\n' \
        "${0##*/}" "${SPIKE_PROJECT}" "${SPIKE_PROJECT}" >&2
  fi
  [[ -z "${SPIKE_TMP}" ]] || rm -rf -- "${SPIKE_TMP}"
  exit "${rc}"
}

# Starts MusicLib 1.1.0 in the spike project with new volumes and random
# passwords (a .env in a private temporary folder, never in the
# repository), prepares /import and /work for the tools user and builds the
# verification tool into /work/bin/spike.
spike_begin() {
  SPIKE_TMP="$(mktemp -d)"
  chmod 700 "${SPIKE_TMP}"
  local env_file="${SPIKE_TMP}/.env"
  (
    umask 077
    printf 'POSTGRES_PASSWORD=%s\nMUSICLIB_PASSWORD=%s\nMUSICLIB_IMAGE=%s\nDEBIAN_IMAGE=%s\n' \
      "$(random_hex)" "$(random_hex)" "${MUSICLIB_IMAGE}" "${DEBIAN_IMAGE}" >"${env_file}"
  )
  SPIKE_ENV="$(native_path "${env_file}")"
  trap spike_end EXIT
  trap 'exit 130' INT TERM

  info "deleting any leftover of the Compose project ${SPIKE_PROJECT}"
  compose_spike down --volumes --remove-orphans --timeout 30 || die "cannot delete the old ${SPIKE_PROJECT} project"
  info "building the tools image"
  compose_spike build tools || die "building the tools image failed"
  info "starting MusicLib (${MUSICLIB_IMAGE}) and PostgreSQL"
  compose_spike up --detach --wait app || die "MusicLib did not become healthy"
  compose_spike run --rm --no-deps -T taggers \
    chown "${VIBRANCE_DEV_UID}:${VIBRANCE_DEV_GID}" /import /work || die "cannot prepare /import and /work"
  info "building the verification tool (scripts/spike)"
  compose_spike run --rm --no-deps -T tools go build -o /work/bin/spike ./scripts/spike \
    || die "building scripts/spike failed"
}

# Runs one subcommand of the verification tool in the tools container.
spike_tool() {
  info "spike $*"
  compose_spike run --rm --no-deps -T tools /work/bin/spike "$@"
}

# Copies standard input to /work/<path> (a fragment or a verdict written by
# the host).
spike_put() {
  # shellcheck disable=SC2016 # $1 is expanded by the container's sh
  compose_spike run --rm --no-deps -T tools \
    sh -c 'mkdir -p "$(dirname "$1")" && cat >"$1"' sh "/work/$1"
}

# Writes the input albums of DESIGN.md §12.2 into /import.
spike_inputs() {
  spike_tool inputs || die "writing the inputs failed"
  info "encoding the MP3s and tagging the M4As (taggers container)"
  compose_spike run --rm --no-deps -T taggers sh /work/taggers.sh || die "the taggers failed"
}

# Imports every input album into MusicLib and waits for the renders.
spike_import() {
  spike_tool import || die "the import failed"
}
